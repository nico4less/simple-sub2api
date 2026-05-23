package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
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
	DisableAccount(accountID string) (bool, error)
}

const permanentFailureStrikeLimit = 3

var cooldownCountdownPattern = regexp.MustCompile(`(?i)(?:try again|retry|available|reset)[^\n\r]{0,80}\b(?:in|after)\s+(?:(\d+)\s*h(?:ours?)?)?\s*(?:(\d+)\s*m(?:in(?:ute)?s?)?)?\s*(?:(\d+)\s*s(?:ec(?:ond)?s?)?)?`)

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
	h.touchKey(matchedKey)
	group := h.resolveGroup(cfg, matchedKey.RoutingPolicy)
	sessionID := extractSessionID(r, group)
	excluded := map[string]bool{}
	maxAttempts := maxGatewayAttempts(group)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		account, state, err := h.Pool.SelectWithPolicyOptions(decision, matchedKey.RoutingPolicy, group, accountpool.SelectOptions{SessionID: sessionID, Excluded: excluded})
		if err != nil {
			h.recordRouting(decision, accountpool.AccountState{}, "no eligible account")
			if lastErr == nil {
				lastErr = err
			}
			break
		}
		h.recordRouting(decision, state, "")
		activeAccountID := account.ID
		h.Pool.IncrementActiveConn(activeAccountID)
		active := true
		defer func() {
			if active {
				h.Pool.DecrementActiveConn(activeAccountID)
			}
		}()
		resp, err := h.forwardAttempt(r, cfg, account, body)
		if err != nil {
			h.Pool.DecrementActiveConn(activeAccountID)
			active = false
			excluded[account.ID] = true
			h.cooldownWithGroup(account.ID, group, 0)
			h.recordRequest(account.ID, matchedKey, parsed.Model, taskType, http.StatusBadGateway, false, "upstream request failed")
			lastErr = err
			continue
		}
		rotateOnStatus := shouldRotateOnStatusCode(resp.StatusCode, group)
		if rotateOnStatus {
			cooldownOverride := retryAfterDuration(resp.Header)
			var replayBody []byte
			if cooldownOverride <= 0 {
				replayBody, cooldownOverride = readCooldownBody(resp.Body)
				resp.Body = io.NopCloser(bytes.NewReader(replayBody))
			}
			excluded[account.ID] = true
			h.cooldownWithGroup(account.ID, group, cooldownOverride)
			h.recordRequest(account.ID, matchedKey, parsed.Model, taskType, resp.StatusCode, false, "upstream returned HTTP "+http.StatusText(resp.StatusCode))
			lastErr = errors.New("upstream returned HTTP " + http.StatusText(resp.StatusCode))
			if attempt < maxAttempts {
				h.recordFailureAndMaybeDisable(account.ID, resp.StatusCode)
				h.Pool.DecrementActiveConn(activeAccountID)
				active = false
				_ = resp.Body.Close()
				continue
			}
		}
		if !rotateOnStatus && upstreamcompat.IsOpenAIErrorStatus(resp.StatusCode) && !isPermanentCredentialFailure(resp.StatusCode) {
			h.cooldownWithGroup(account.ID, group, retryAfterDuration(resp.Header))
		}
		w.Header().Set("X-Simple-Sub2API-Attempts", strconv.Itoa(attempt))
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			h.Pool.MarkSuccess(account.ID)
		} else {
			h.recordFailureAndMaybeDisable(account.ID, resp.StatusCode)
		}
		if parsed.Stream {
			h.writeStream(w, resp, state.AccountID, matchedKey, parsed.Model, taskType)
			h.Pool.DecrementActiveConn(activeAccountID)
			active = false
			_ = resp.Body.Close()
			return
		}
		h.writeNonStream(w, resp, state.AccountID, matchedKey, parsed.Model, taskType)
		h.Pool.DecrementActiveConn(activeAccountID)
		active = false
		_ = resp.Body.Close()
		return
	}
	message := "no eligible upstream account"
	if lastErr != nil {
		message = "group pool exhausted: " + lastErr.Error()
	}
	h.recordRequest("", matchedKey, parsed.Model, taskType, http.StatusServiceUnavailable, false, message)
	writeOpenAIError(w, http.StatusServiceUnavailable, message, "server_error", "group_exhausted")
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

func (h Handler) forwardAttempt(r *http.Request, cfg config.Config, account config.Account, body []byte) (*http.Response, error) {
	if h.Pool == nil {
		return nil, errors.New("account pool unavailable")
	}
	client, err := clientForAccount(cfg, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := upstreamcompat.BuildUpstreamRequest(r, account, body)
	if err != nil {
		return nil, err
	}
	return client.Do(upstreamReq)
}

func (h Handler) resolveGroup(cfg config.Config, policy config.KeyRoutingPolicy) *config.Group {
	if policy.Mode == "groups" && len(policy.GroupIDs) > 0 {
		for _, groupID := range policy.GroupIDs {
			if group := findActiveGroup(cfg.Groups, groupID); group != nil {
				return group
			}
		}
	}
	for i := range cfg.Groups {
		if cfg.Groups[i].Status == "active" && len(cfg.Groups[i].AccountIDs) > 0 {
			return &cfg.Groups[i]
		}
	}
	return nil
}

func findActiveGroup(groups []config.Group, groupID string) *config.Group {
	for i := range groups {
		if groups[i].ID == groupID && groups[i].Status == "active" {
			return &groups[i]
		}
	}
	return nil
}

func maxGatewayAttempts(group *config.Group) int {
	maxAttempts := 1
	if group != nil && group.RotationPolicy.RetryOnErrors {
		maxAttempts = len(group.AccountIDs)
		if maxAttempts > 3 {
			maxAttempts = 3
		}
		if maxAttempts < 1 {
			maxAttempts = 1
		}
	}
	return maxAttempts
}

func shouldRotateOnStatusCode(status int, group *config.Group) bool {
	if group == nil || !group.RotationPolicy.RetryOnErrors {
		return false
	}
	for _, code := range group.RotationPolicy.RotateErrorCodes {
		if code == status {
			return true
		}
	}
	return false
}

func extractSessionID(r *http.Request, group *config.Group) string {
	if group == nil || !group.RotationPolicy.StickySessionsEnabled {
		return ""
	}
	header := strings.TrimSpace(group.RotationPolicy.StickyHeader)
	if header == "" {
		header = "X-Session-ID"
	}
	if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
		return value
	}
	if authorization := strings.TrimSpace(r.Header.Get("Authorization")); authorization != "" {
		sum := sha256.Sum256([]byte(authorization))
		return "auth:" + hex.EncodeToString(sum[:])
	}
	return ""
}

func retryAfterDuration(header http.Header) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value != "" {
		if duration := parseCooldownTime(value, false); duration > 0 {
			return duration
		}
	}
	reset := strings.TrimSpace(header.Get("X-RateLimit-Reset"))
	if reset != "" {
		if duration := parseCooldownTime(reset, true); duration > 0 {
			return duration
		}
	}
	return 0
}

func TestRetryAfterDuration(header http.Header) time.Duration {
	return retryAfterDuration(header)
}

func parseCooldownTime(value string, allowUnixTimestamp bool) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		if allowUnixTimestamp && seconds > 86400 {
			duration := time.Until(time.Unix(int64(seconds), 0))
			if duration > 0 {
				return duration
			}
		}
		return time.Duration(seconds) * time.Second
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil && allowUnixTimestamp && unix > 0 {
		duration := time.Until(time.Unix(unix, 0))
		if duration > 0 {
			return duration
		}
	}
	if at, err := http.ParseTime(value); err == nil {
		duration := time.Until(at)
		if duration > 0 {
			return duration
		}
	}
	return 0
}

func readCooldownBody(body io.Reader) ([]byte, time.Duration) {
	payload, err := io.ReadAll(io.LimitReader(body, 64<<10))
	if err != nil {
		return payload, 0
	}
	return payload, parseCooldownCountdown(string(payload))
}

func TestReadCooldownBody(body io.Reader) ([]byte, time.Duration) {
	return readCooldownBody(body)
}

func parseCooldownCountdown(text string) time.Duration {
	match := cooldownCountdownPattern.FindStringSubmatch(text)
	if len(match) == 0 {
		return 0
	}
	hours := atoiDefault(match[1])
	minutes := atoiDefault(match[2])
	seconds := atoiDefault(match[3])
	duration := time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second
	return duration
}

func atoiDefault(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
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
	h.cooldownWithGroup(accountID, nil, 0)
}

func (h Handler) cooldownWithGroup(accountID string, group *config.Group, override time.Duration) {
	duration := 30 * time.Second
	if group != nil {
		duration = time.Duration(group.RotationPolicy.CooldownDurationSeconds) * time.Second
	}
	if override > 0 {
		duration = override
	}
	if h.Pool != nil && accountID != "" {
		h.Pool.Cooldown(accountID, time.Now().UTC().Add(duration))
	}
	if h.Metrics != nil {
		h.Metrics.RecordCooldown(accountID)
	}
	if h.Logger != nil {
		h.Logger.Warn("gateway upstream account cooldown", "account_id", accountID)
	}
}

func (h Handler) recordFailureAndMaybeDisable(accountID string, statusCode int) {
	if h.Pool == nil || h.Store == nil || !isPermanentCredentialFailure(statusCode) {
		return
	}
	strikes := h.Pool.RecordFailure(accountID, true)
	if strikes < permanentFailureStrikeLimit {
		return
	}
	disabled, err := h.Store.DisableAccount(accountID)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("gateway account auto-disable failed", "account_id", accountID, "error", err.Error())
		}
		return
	}
	if disabled && h.Logger != nil {
		h.Logger.Warn("gateway account auto-disabled after permanent failures", "account_id", accountID, "strikes", strikes)
	}
	if disabled {
		h.Pool.DisableAccount(accountID)
	}
}

func isPermanentCredentialFailure(statusCode int) bool {
	return statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
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
