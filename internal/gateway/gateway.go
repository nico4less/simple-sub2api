package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/accountpool"
	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/gatewayauth"
	"github.com/0xForce-Network/simple-sub2api/internal/metrics"
	"github.com/0xForce-Network/simple-sub2api/internal/proxyclient"
	"github.com/0xForce-Network/simple-sub2api/internal/routing"
	"github.com/0xForce-Network/simple-sub2api/internal/upstreamcompat"
)

type Store interface {
	Snapshot() config.Config
	TouchGatewayKeyLastUsed(id string, usedAt string) error
}

type Handler struct {
	Store   Store
	Pool    *accountpool.Manager
	Metrics *metrics.Recorder
	Logger  *slog.Logger
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		writeOpenAIError(w, http.StatusNotFound, "gateway route not found", "not_found_error", "route_not_found")
		return
	}
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
		return
	}
	if h.Pool == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "account pool unavailable", "server_error", "account_pool_unavailable")
		return
	}
	parsed, body, err := upstreamcompat.ParseChatCompletionRequest(r.Body, 4<<20)
	if err != nil {
		h.recordRequest("", config.GatewayKey{}, "", "", http.StatusBadRequest, false, err.Error())
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error", "invalid_request")
		return
	}
	cfg := h.Store.Snapshot()
	matchedKey, ok := gatewayauth.MatchedGatewayKey(r)
	if !ok {
		matchedKey = config.GatewayKey{ID: "legacy", RoutingPolicy: config.KeyRoutingPolicy{Mode: "all_enabled"}}
	}
	taskType := taskTypeFromRequest(r, parsed)
	decision := routing.Decide(cfg.Routing, routing.Request{TaskType: taskType, Model: parsed.Model, Tags: tagsFromRequest(r)})
	account, state, err := h.Pool.SelectWithPolicy(decision, matchedKey.RoutingPolicy)
	if err != nil {
		h.recordRouting(decision, accountpool.AccountState{}, "no eligible account")
		h.recordRequest("", matchedKey, parsed.Model, taskType, http.StatusServiceUnavailable, false, "no eligible upstream account")
		writeOpenAIError(w, http.StatusServiceUnavailable, "no eligible upstream account", "server_error", "no_eligible_account")
		return
	}
	h.touchKey(matchedKey)
	h.recordRouting(decision, state, "")
	client, err := clientForAccount(cfg, account)
	if err != nil {
		h.cooldown(account.ID)
		h.recordRequest(account.ID, matchedKey, parsed.Model, taskType, http.StatusBadGateway, false, "configured proxy is unavailable")
		writeOpenAIError(w, http.StatusBadGateway, "configured proxy is unavailable", "api_error", "proxy_unavailable")
		return
	}
	upstreamReq, err := upstreamcompat.BuildUpstreamRequest(r, account, body)
	if err != nil {
		h.recordRequest(account.ID, matchedKey, parsed.Model, taskType, http.StatusBadRequest, false, "invalid upstream account configuration")
		writeOpenAIError(w, http.StatusBadRequest, "invalid upstream account configuration", "invalid_request_error", "invalid_upstream")
		return
	}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		h.cooldown(account.ID)
		h.recordRequest(account.ID, matchedKey, parsed.Model, taskType, http.StatusBadGateway, false, "upstream request failed")
		writeOpenAIError(w, http.StatusBadGateway, "upstream request failed", "api_error", "upstream_request_failed")
		return
	}
	defer resp.Body.Close()
	if upstreamcompat.IsOpenAIErrorStatus(resp.StatusCode) {
		h.cooldown(account.ID)
	}
	if parsed.Stream {
		h.writeStream(w, resp, state.AccountID, matchedKey, parsed.Model, taskType)
		return
	}
	h.writeNonStream(w, resp, state.AccountID, matchedKey, parsed.Model, taskType)
}

func clientForAccount(cfg config.Config, account config.Account) (*http.Client, error) {
	specs := proxyclient.SpecsFromConfig(cfg)
	spec, _, err := proxyclient.Resolve(account, specs)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.Probe.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return proxyclient.HTTPClient(spec, timeout)
}

func (h Handler) writeNonStream(w http.ResponseWriter, resp *http.Response, accountID string, key config.GatewayKey, model string, taskType string) {
	copySafeHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", contentType(resp.Header.Get("Content-Type"), "application/json"))
	w.Header().Set("X-Simple-Sub2API-Account", accountID)
	w.WriteHeader(resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && h.Pool != nil {
		_, _, crossedQuotaSwitch := h.Pool.AddUsage(accountID, usageTokens(body))
		if crossedQuotaSwitch && h.Metrics != nil {
			h.Metrics.RecordQuotaSwitch(accountID)
		}
	}
	h.recordRequest(accountID, key, model, taskType, resp.StatusCode, resp.StatusCode >= 200 && resp.StatusCode < 300, "upstream returned HTTP "+http.StatusText(resp.StatusCode))
	_, _ = w.Write(body)
}

func (h Handler) writeStream(w http.ResponseWriter, resp *http.Response, accountID string, key config.GatewayKey, model string, taskType string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(w, resp.Body)
		h.recordRequest(accountID, key, model, taskType, resp.StatusCode, false, "upstream returned HTTP "+http.StatusText(resp.StatusCode))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	_ = upstreamcompat.ForEachOpenAISSEDataPayloadFromReader(resp.Body, func(payload []byte) {
		_ = upstreamcompat.WriteSSEData(w, payload)
		if flusher != nil {
			flusher.Flush()
		}
	})
	_ = upstreamcompat.WriteSSEDone(w)
	if flusher != nil {
		flusher.Flush()
	}
	h.recordRequest(accountID, key, model, taskType, resp.StatusCode, true, "")
}

func (h Handler) cooldown(accountID string) {
	if h.Pool != nil && accountID != "" {
		h.Pool.Cooldown(accountID, time.Now().UTC().Add(30*time.Second))
	}
	if h.Metrics != nil {
		h.Metrics.RecordCooldown(accountID)
	}
	if h.Logger != nil {
		h.Logger.Warn("gateway upstream account cooldown", "account_id", accountID)
	}
}

func (h Handler) touchKey(key config.GatewayKey) {
	if h.Store == nil || key.ID == "" || key.ID == "legacy" {
		return
	}
	if err := h.Store.TouchGatewayKeyLastUsed(key.ID, time.Now().UTC().Format(time.RFC3339)); err != nil && h.Logger != nil {
		h.Logger.Warn("gateway key last-used update failed", "key_id", key.ID, "error", err.Error())
	}
}

func (h Handler) recordRouting(decision routing.Decision, state accountpool.AccountState, reason string) {
	if h.Metrics == nil {
		return
	}
	h.Metrics.RecordRoutingDecision(metrics.RoutingDecision{
		TaskType:        decision.TaskType,
		MatchedRule:     decision.MatchedRule,
		PreferTiers:     decision.PreferTiers,
		FallbackTiers:   decision.FallbackTiers,
		SelectedTier:    state.Tier,
		SelectedAccount: state.AccountID,
		Reason:          reason,
	})
}

func (h Handler) recordRequest(accountID string, key config.GatewayKey, model string, taskType string, statusCode int, success bool, message string) {
	if h.Metrics == nil {
		return
	}
	h.Metrics.RecordRequest(metrics.RequestResult{AccountID: accountID, GatewayKeyID: key.ID, GatewayKeyRef: key.Preview, Model: model, TaskType: taskType, StatusCode: statusCode, Success: success, Error: message})
}

func taskTypeFromRequest(r *http.Request, parsed upstreamcompat.ChatCompletionRequest) string {
	if value := strings.TrimSpace(r.Header.Get("X-Simple-Task-Type")); value != "" {
		return value
	}
	if value, ok := parsed.Metadata["task_type"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func tagsFromRequest(r *http.Request) []string {
	raw := strings.TrimSpace(r.Header.Get("X-Simple-Tags"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

func writeOpenAIError(w http.ResponseWriter, status int, message string, typ string, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(upstreamcompat.ErrorEnvelope(message, typ, code))
}

func copySafeHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if canonical == "Authorization" || canonical == "Set-Cookie" || canonical == "Content-Length" || canonical == "Connection" || canonical == "Transfer-Encoding" {
			continue
		}
		for _, value := range values {
			dst.Add(canonical, value)
		}
	}
}

func contentType(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func usageTokens(body []byte) int64 {
	var envelope struct {
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0
	}
	if envelope.Usage.TotalTokens < 0 {
		return 0
	}
	return envelope.Usage.TotalTokens
}
