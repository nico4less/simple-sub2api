package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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
	UpdateOpenAIAccessToken(accountID string, token config.OpenAIAccessTokenUpdate) (config.Account, bool, error)
	UpdateAnthropicAccessToken(accountID string, token config.AnthropicAccessTokenUpdate) (config.Account, bool, error)
	UpdateAccountMetadata(accountID string, update config.AccountMetadataUpdate) (config.Account, bool, error)
}

const permanentFailureStrikeLimit = 3
const openAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
const openAITokenURL = "https://auth.openai.com/oauth/token"
const anthropicClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
const anthropicTokenURL = "https://platform.claude.com/v1/oauth/token"

var cooldownCountdownPattern = regexp.MustCompile(`(?i)(?:try again|retry|available|reset)[^\n\r]{0,80}\b(?:in|after)\s+(?:(\d+)\s*h(?:ours?)?)?\s*(?:(\d+)\s*m(?:in(?:ute)?s?)?)?\s*(?:(\d+)\s*s(?:ec(?:ond)?s?)?)?`)

type openAITokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
}

type anthropicTokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
}

type openAICodexUsageSnapshot struct {
	PrimaryUsedPercent          *float64 `json:"primary_used_percent,omitempty"`
	PrimaryResetAfterSeconds    *int     `json:"primary_reset_after_seconds,omitempty"`
	PrimaryWindowMinutes        *int     `json:"primary_window_minutes,omitempty"`
	SecondaryUsedPercent        *float64 `json:"secondary_used_percent,omitempty"`
	SecondaryResetAfterSeconds  *int     `json:"secondary_reset_after_seconds,omitempty"`
	SecondaryWindowMinutes      *int     `json:"secondary_window_minutes,omitempty"`
	PrimaryOverSecondaryPercent *float64 `json:"primary_over_secondary_percent,omitempty"`
	UpdatedAt                   string   `json:"updated_at,omitempty"`
}

type normalizedCodexLimits struct {
	Used5hPercent   *float64
	Reset5hSeconds  *int
	Window5hMinutes *int
	Used7dPercent   *float64
	Reset7dSeconds  *int
	Window7dMinutes *int
}

func (s *openAICodexUsageSnapshot) normalize() *normalizedCodexLimits {
	if s == nil {
		return nil
	}
	result := &normalizedCodexLimits{}
	primaryMins, secondaryMins := 0, 0
	hasPrimaryWindow, hasSecondaryWindow := false, false
	if s.PrimaryWindowMinutes != nil {
		primaryMins = *s.PrimaryWindowMinutes
		hasPrimaryWindow = true
	}
	if s.SecondaryWindowMinutes != nil {
		secondaryMins = *s.SecondaryWindowMinutes
		hasSecondaryWindow = true
	}
	use5hFromPrimary, use7dFromPrimary := false, false
	if hasPrimaryWindow && hasSecondaryWindow {
		if primaryMins < secondaryMins {
			use5hFromPrimary = true
		} else {
			use7dFromPrimary = true
		}
	} else if hasPrimaryWindow {
		if primaryMins <= 360 {
			use5hFromPrimary = true
		} else {
			use7dFromPrimary = true
		}
	} else if hasSecondaryWindow {
		if secondaryMins <= 360 {
			use7dFromPrimary = true
		} else {
			use5hFromPrimary = true
		}
	} else {
		use7dFromPrimary = true
	}
	if use5hFromPrimary {
		result.Used5hPercent = s.PrimaryUsedPercent
		result.Reset5hSeconds = s.PrimaryResetAfterSeconds
		result.Window5hMinutes = s.PrimaryWindowMinutes
		result.Used7dPercent = s.SecondaryUsedPercent
		result.Reset7dSeconds = s.SecondaryResetAfterSeconds
		result.Window7dMinutes = s.SecondaryWindowMinutes
	} else if use7dFromPrimary {
		result.Used7dPercent = s.PrimaryUsedPercent
		result.Reset7dSeconds = s.PrimaryResetAfterSeconds
		result.Window7dMinutes = s.PrimaryWindowMinutes
		result.Used5hPercent = s.SecondaryUsedPercent
		result.Reset5hSeconds = s.SecondaryResetAfterSeconds
		result.Window5hMinutes = s.SecondaryWindowMinutes
	}
	return result
}

type Handler struct {
	Store    Store
	Pool     *accountpool.Manager
	Metrics  *metrics.Recorder
	Logger   *slog.Logger
	DebugAPI bool
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	matchedKey, ok := gatewayauth.MatchedGatewayKey(r)
	if !ok {
		matchedKey = config.GatewayKey{ID: "legacy", RoutingPolicy: config.KeyRoutingPolicy{Mode: "all_enabled"}}
	}
	normalizeGatewayRequestPath(r)
	if r.URL.Path != "/v1/chat/completions" && r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/messages" {
		h.logAPIDebugRequest(r, matchedKey, nil, upstreamcompat.ChatCompletionRequest{}, errors.New("gateway route not found"))
		writeOpenAIError(w, http.StatusNotFound, "gateway route not found", "not_found_error", "route_not_found")
		return
	}
	if r.Method != http.MethodPost {
		h.logAPIDebugRequest(r, matchedKey, nil, upstreamcompat.ChatCompletionRequest{}, errors.New("method not allowed"))
		writeOpenAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "method_not_allowed")
		return
	}
	if h.Pool == nil {
		h.logAPIDebugRequest(r, matchedKey, nil, upstreamcompat.ChatCompletionRequest{}, errors.New("account pool unavailable"))
		writeOpenAIError(w, http.StatusServiceUnavailable, "account pool unavailable", "server_error", "account_pool_unavailable")
		return
	}
	requestPath := r.URL.Path
	parsed, body, err := upstreamcompat.ParseChatCompletionRequest(r.Body, 4<<20)
	h.logAPIDebugRequest(r, matchedKey, body, parsed, err)
	if err != nil {
		h.recordRequest("", matchedKey, "", "", http.StatusBadRequest, false, err.Error())
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error", "invalid_request")
		return
	}
	cfg := h.Store.Snapshot()
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
			if refreshed, refreshErr := h.refreshDisabledOpenAIOAuthAccount(r.Context(), cfg, group, matchedKey.RoutingPolicy); refreshErr == nil && refreshed.ID != "" {
				account, state, err = h.Pool.SelectWithPolicyOptions(decision, matchedKey.RoutingPolicy, group, accountpool.SelectOptions{SessionID: sessionID, Excluded: excluded})
			}
		}
		if err != nil {
			h.logNoEligibleAccount(requestPath, parsed.Model, taskType, matchedKey, decision, group, excluded, attempt, maxAttempts, err)
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
			if shouldCooldownUpstreamFailure(requestPath, err) {
				h.cooldownWithGroupReason(account.ID, group, 0, "upstream_request_failed", http.StatusBadGateway)
			}
			h.recordRequest(account.ID, matchedKey, parsed.Model, taskType, http.StatusBadGateway, false, "upstream request failed")
			lastErr = err
			continue
		}
		if shouldRefreshOpenAIOAuthAfterAuthFailure(requestPath, account, resp.StatusCode) {
			refreshed, refreshErr := h.refreshOpenAIAccessTokenForAccount(r.Context(), cfg, account, nil)
			if refreshErr == nil {
				_ = resp.Body.Close()
				h.Pool.DecrementActiveConn(activeAccountID)
				active = false
				account = refreshed
				activeAccountID = account.ID
				h.Pool.IncrementActiveConn(activeAccountID)
				active = true
				resp, err = h.forwardAttempt(r, cfg, account, body)
				if err != nil {
					h.Pool.DecrementActiveConn(activeAccountID)
					active = false
					excluded[account.ID] = true
					if shouldCooldownUpstreamFailure(requestPath, err) {
						h.cooldownWithGroupReason(account.ID, group, 0, "upstream_request_failed", http.StatusBadGateway)
					}
					h.recordRequest(account.ID, matchedKey, parsed.Model, taskType, http.StatusBadGateway, false, "upstream request failed")
					lastErr = err
					continue
				}
			} else if h.Logger != nil {
				h.Logger.Warn("openai_oauth_auth_failure_refresh_failed", "account_id", account.ID, "error", sanitizeCredentialError(refreshErr))
			}
		}
		rotateOnStatus := shouldRotateAttemptStatus(requestPath, account, resp.StatusCode, group)
		if rotateOnStatus {
			cooldownOverride := retryAfterDuration(resp.Header)
			var replayBody []byte
			replayBody, bodyCooldown := readCooldownBody(resp.Body)
			resp.Body = io.NopCloser(bytes.NewReader(replayBody))
			if cooldownOverride <= 0 {
				cooldownOverride = bodyCooldown
			}
			excluded[account.ID] = true
			h.logUpstreamError(requestPath, parsed.Model, taskType, matchedKey, group, account.ID, resp.StatusCode, attempt, maxAttempts, true, replayBody, resp.Header)
			h.cooldownWithGroupReason(account.ID, group, cooldownOverride, "rotate_on_status", resp.StatusCode)
			h.recordRequest(account.ID, matchedKey, parsed.Model, taskType, resp.StatusCode, false, "upstream returned HTTP "+http.StatusText(resp.StatusCode))
			lastErr = errors.New("upstream returned HTTP " + http.StatusText(resp.StatusCode))
			if attempt < maxAttempts {
				if shouldAutoDisable(requestPath, account, resp.StatusCode) {
					h.recordFailureAndMaybeDisable(account, requestPath, resp.StatusCode)
				}
				h.Pool.DecrementActiveConn(activeAccountID)
				active = false
				_ = resp.Body.Close()
				continue
			}
		}
		if !rotateOnStatus && upstreamcompat.IsOpenAIErrorStatus(resp.StatusCode) {
			var replayBody []byte
			if h.DebugAPI {
				replayBody, _ = readCooldownBody(resp.Body)
				resp.Body = io.NopCloser(bytes.NewReader(replayBody))
			}
			h.logUpstreamError(requestPath, parsed.Model, taskType, matchedKey, group, account.ID, resp.StatusCode, attempt, maxAttempts, false, replayBody, resp.Header)
			if !isPermanentCredentialFailure(resp.StatusCode) && shouldCooldownStatus(requestPath, account, resp.StatusCode) {
				h.cooldownWithGroupReason(account.ID, group, retryAfterDuration(resp.Header), "upstream_error_status", resp.StatusCode)
			}
		}
		w.Header().Set("X-Simple-Sub2API-Attempts", strconv.Itoa(attempt))
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			h.Pool.MarkSuccess(account.ID)
		} else if shouldAutoDisable(requestPath, account, resp.StatusCode) {
			h.recordFailureAndMaybeDisable(account, requestPath, resp.StatusCode)
		}
		h.persistOpenAICodexUsageSnapshot(account, resp.Header)
		if parsed.Stream {
			h.writeStream(w, r, resp, state.AccountID, matchedKey, parsed.Model, taskType)
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
	h.logGroupExhausted(requestPath, parsed.Model, taskType, matchedKey, decision, group, excluded, maxAttempts, message)
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
	account, err = h.accountWithFreshOpenAIAccessToken(r.Context(), cfg, account, client)
	if err != nil {
		return nil, err
	}
	account, err = h.accountWithFreshAnthropicAccessToken(r.Context(), cfg, account, client)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := upstreamcompat.BuildUpstreamRequest(r, account, body)
	if err != nil {
		return nil, err
	}
	h.logAPIDebugUpstreamRequest(r, upstreamReq, account)
	return client.Do(upstreamReq)
}

func (h Handler) refreshDisabledOpenAIOAuthAccount(ctx context.Context, cfg config.Config, group *config.Group, policy config.KeyRoutingPolicy) (config.Account, error) {
	if h.Store == nil || h.Pool == nil {
		return config.Account{}, errors.New("store or pool unavailable")
	}
	for _, account := range cfg.Accounts {
		if account.Enabled || !matchesRefreshRescuePolicy(account, group, policy) || !openAIOAuthCanRefresh(account) {
			continue
		}
		client, err := clientForAccount(cfg, account)
		if err != nil {
			return config.Account{}, err
		}
		refreshed, err := h.accountWithFreshOpenAIAccessToken(ctx, cfg, account, client)
		if err != nil {
			return config.Account{}, err
		}
		h.reEnableRefreshedAccount(refreshed)
		return refreshed, nil
	}
	return config.Account{}, errors.New("no disabled openai oauth account can be refreshed")
}

func matchesRefreshRescuePolicy(account config.Account, group *config.Group, policy config.KeyRoutingPolicy) bool {
	mode := strings.TrimSpace(policy.Mode)
	if mode == "" {
		mode = "all_enabled"
	}
	switch mode {
	case "groups":
		if group == nil {
			return len(policy.GroupIDs) == 0
		}
		for _, id := range group.AccountIDs {
			if id == account.ID {
				return true
			}
		}
		return false
	case "account_ids":
		if len(policy.AccountIDs) == 0 {
			return true
		}
		for _, id := range policy.AccountIDs {
			if id == account.ID {
				return true
			}
		}
		return false
	case "tags":
		if len(policy.Tags) == 0 {
			return true
		}
		return refreshRescueHasAnyTag(account.Tags, policy.Tags)
	default:
		return true
	}
}

func refreshRescueHasAnyTag(accountTags []string, policyTags []string) bool {
	seen := map[string]bool{}
	for _, tag := range accountTags {
		seen[strings.ToLower(strings.TrimSpace(tag))] = true
	}
	for _, tag := range policyTags {
		if seen[strings.ToLower(strings.TrimSpace(tag))] {
			return true
		}
	}
	return false
}

func openAIOAuthCanRefresh(account config.Account) bool {
	return config.AccountPlatform(account) == "openai" && strings.TrimSpace(account.Type) == "oauth" && credentialValue(account.Credential, "refresh_token") != ""
}

func anthropicOAuthCanRefresh(account config.Account) bool {
	accountType := strings.TrimSpace(account.Type)
	return config.AccountPlatform(account) == "anthropic" && (accountType == "oauth" || accountType == "setup-token") && credentialValue(account.Credential, "refresh_token") != ""
}

func (h Handler) reEnableRefreshedAccount(account config.Account) {
	if h.Pool == nil || strings.TrimSpace(account.ID) == "" {
		return
	}
	h.Pool.EnableAccount(account)
	if h.Logger != nil {
		h.Logger.Info("openai_oauth_refresh_reenabled_account", "account_id", account.ID)
	}
}

func (h Handler) accountWithFreshOpenAIAccessToken(ctx context.Context, cfg config.Config, account config.Account, client *http.Client) (config.Account, error) {
	if config.AccountPlatform(account) != "openai" || strings.TrimSpace(account.Type) != "oauth" {
		return account, nil
	}
	if token := upstreamcompat.APIKeyFromCredential(account.Credential); token != "" && tokenNotExpired(account.Credential, 2*time.Minute) {
		if !account.Enabled {
			account = h.persistExistingOpenAIAccessToken(account, token)
		}
		return account, nil
	}
	refreshToken := credentialValue(account.Credential, "refresh_token")
	if refreshToken == "" {
		return account, nil
	}
	return h.refreshOpenAIAccessTokenForAccount(ctx, cfg, account, client)
}

func (h Handler) accountWithFreshAnthropicAccessToken(ctx context.Context, cfg config.Config, account config.Account, client *http.Client) (config.Account, error) {
	if !anthropicOAuthCanRefresh(account) {
		return account, nil
	}
	if token := credentialValue(account.Credential, "access_token"); token != "" && tokenNotExpired(account.Credential, 2*time.Minute) {
		return account, nil
	}
	return h.refreshAnthropicAccessTokenForAccount(ctx, cfg, account, client)
}

func (h Handler) refreshAnthropicAccessTokenForAccount(ctx context.Context, cfg config.Config, account config.Account, client *http.Client) (config.Account, error) {
	if !anthropicOAuthCanRefresh(account) {
		return account, nil
	}
	refreshToken := credentialValue(account.Credential, "refresh_token")
	if refreshToken == "" {
		return account, nil
	}
	if client == nil {
		var err error
		client, err = clientForAccount(cfg, account)
		if err != nil {
			return account, err
		}
	}
	clientID := credentialValue(account.Credential, "client_id")
	if clientID == "" {
		clientID = anthropicClientID
	}
	tokenURL := credentialValue(account.Credential, "token_url")
	if tokenURL == "" {
		tokenURL = anthropicTokenURL
	}
	if h.Logger != nil {
		h.Logger.Info("anthropic_oauth_refresh_start", "account_id", account.ID, "has_access_token", credentialValue(account.Credential, "access_token") != "", "client_id_configured", credentialValue(account.Credential, "client_id") != "")
	}
	token, err := refreshAnthropicAccessToken(ctx, client, tokenURL, refreshToken, clientID)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("anthropic_oauth_refresh_failed", "account_id", account.ID, "error", sanitizeCredentialError(err))
		}
		return account, err
	}
	updated := config.AnthropicAccessTokenUpdate{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Scope:        token.Scope,
		ExpiresAt:    time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	if updated.ExpiresAt == "" || token.ExpiresIn <= 0 {
		updated.ExpiresAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	}
	credentialUpdates := map[string]string{
		"access_token": updated.AccessToken,
		"expires_at":   updated.ExpiresAt,
	}
	if updated.RefreshToken != "" {
		credentialUpdates["refresh_token"] = updated.RefreshToken
	}
	if updated.TokenType != "" {
		credentialUpdates["token_type"] = updated.TokenType
	}
	if updated.Scope != "" {
		credentialUpdates["scope"] = updated.Scope
	}
	account.Credential = mergeCredential(account.Credential, credentialUpdates)
	if h.Store != nil {
		if persisted, ok, persistErr := h.Store.UpdateAnthropicAccessToken(account.ID, updated); persistErr != nil {
			if h.Logger != nil {
				h.Logger.Warn("anthropic_oauth_refresh_persist_failed", "account_id", account.ID, "error", persistErr.Error())
			}
		} else if ok {
			account = persisted
		}
	}
	if h.Pool != nil {
		h.Pool.UpdateAccount(account)
	}
	if h.Logger != nil {
		h.Logger.Info("anthropic_oauth_refresh_success", "account_id", account.ID, "expires_at", updated.ExpiresAt, "rotated_refresh_token", updated.RefreshToken != "")
	}
	return account, nil
}

func (h Handler) refreshOpenAIAccessTokenForAccount(ctx context.Context, cfg config.Config, account config.Account, client *http.Client) (config.Account, error) {
	if config.AccountPlatform(account) != "openai" || strings.TrimSpace(account.Type) != "oauth" {
		return account, nil
	}
	refreshToken := credentialValue(account.Credential, "refresh_token")
	if refreshToken == "" {
		return account, nil
	}
	if client == nil {
		var err error
		client, err = clientForAccount(cfg, account)
		if err != nil {
			return account, err
		}
	}
	clientID := credentialValue(account.Credential, "client_id")
	if clientID == "" {
		clientID = openAIClientID
	}
	tokenURL := credentialValue(account.Credential, "token_url")
	if tokenURL == "" {
		tokenURL = openAITokenURL
	}
	if h.Logger != nil {
		h.Logger.Info("openai_oauth_refresh_start", "account_id", account.ID, "has_access_token", credentialValue(account.Credential, "access_token") != "", "client_id_configured", credentialValue(account.Credential, "client_id") != "")
	}
	token, err := refreshOpenAIAccessToken(ctx, client, tokenURL, refreshToken, clientID)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warn("openai_oauth_refresh_failed", "account_id", account.ID, "error", sanitizeCredentialError(err))
		}
		return account, err
	}
	updated := config.OpenAIAccessTokenUpdate{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		IDToken:      token.IDToken,
		ExpiresAt:    time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	if updated.ExpiresAt == "" || token.ExpiresIn <= 0 {
		updated.ExpiresAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	}
	account.Credential = mergeCredential(account.Credential, map[string]string{
		"access_token": updated.AccessToken,
		"expires_at":   updated.ExpiresAt,
	})
	if updated.RefreshToken != "" {
		account.Credential = mergeCredential(account.Credential, map[string]string{"refresh_token": updated.RefreshToken})
	}
	if updated.IDToken != "" {
		account.Credential = mergeCredential(account.Credential, map[string]string{"id_token": updated.IDToken})
	}
	if h.Store != nil {
		if persisted, ok, persistErr := h.Store.UpdateOpenAIAccessToken(account.ID, updated); persistErr != nil {
			if h.Logger != nil {
				h.Logger.Warn("openai_oauth_refresh_persist_failed", "account_id", account.ID, "error", persistErr.Error())
			}
		} else if ok {
			account = persisted
		}
	}
	if h.Pool != nil {
		h.Pool.UpdateAccount(account)
	}
	if h.Logger != nil {
		h.Logger.Info("openai_oauth_refresh_success", "account_id", account.ID, "expires_at", updated.ExpiresAt, "rotated_refresh_token", updated.RefreshToken != "")
	}
	return account, nil
}

func (h Handler) persistExistingOpenAIAccessToken(account config.Account, accessToken string) config.Account {
	if h.Store == nil || strings.TrimSpace(account.ID) == "" || strings.TrimSpace(accessToken) == "" {
		return account
	}
	update := config.OpenAIAccessTokenUpdate{
		AccessToken:  accessToken,
		RefreshToken: credentialValue(account.Credential, "refresh_token"),
		IDToken:      credentialValue(account.Credential, "id_token"),
		ExpiresAt:    credentialValue(account.Credential, "expires_at"),
	}
	if strings.TrimSpace(update.ExpiresAt) == "" {
		update.ExpiresAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	}
	if persisted, ok, err := h.Store.UpdateOpenAIAccessToken(account.ID, update); err != nil {
		if h.Logger != nil {
			h.Logger.Warn("openai_oauth_existing_access_token_reenable_failed", "account_id", account.ID, "error", err.Error())
		}
	} else if ok {
		account = persisted
		if h.Logger != nil {
			h.Logger.Info("openai_oauth_existing_access_token_reenabled_account", "account_id", account.ID)
		}
	}
	return account
}

func refreshOpenAIAccessToken(ctx context.Context, client *http.Client, tokenURL string, refreshToken string, clientID string) (openAITokenRefreshResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if strings.TrimSpace(tokenURL) == "" {
		tokenURL = openAITokenURL
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return openAITokenRefreshResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "codex-cli/0.91.0")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return openAITokenRefreshResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return openAITokenRefreshResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return openAITokenRefreshResponse{}, errors.New("token refresh failed: status " + strconv.Itoa(resp.StatusCode) + ", body: " + sanitizeCredentialText(string(body)))
	}
	var token openAITokenRefreshResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return openAITokenRefreshResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return openAITokenRefreshResponse{}, errors.New("token refresh response missing access_token")
	}
	return token, nil
}

func refreshAnthropicAccessToken(ctx context.Context, client *http.Client, tokenURL string, refreshToken string, clientID string) (anthropicTokenRefreshResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if strings.TrimSpace(tokenURL) == "" {
		tokenURL = anthropicTokenURL
	}
	payload, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
	})
	if err != nil {
		return anthropicTokenRefreshResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(payload))
	if err != nil {
		return anthropicTokenRefreshResponse{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "axios/1.13.6")
	resp, err := client.Do(req)
	if err != nil {
		return anthropicTokenRefreshResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return anthropicTokenRefreshResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return anthropicTokenRefreshResponse{}, errors.New("token refresh failed: status " + strconv.Itoa(resp.StatusCode) + ", body: " + sanitizeCredentialText(string(body)))
	}
	var token anthropicTokenRefreshResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return anthropicTokenRefreshResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return anthropicTokenRefreshResponse{}, errors.New("token refresh response missing access_token")
	}
	return token, nil
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

func shouldRotateAttemptStatus(requestPath string, account config.Account, status int, group *config.Group) bool {
	if upstreamcompat.IsOpenAIOAuthAccount(account) && requestPath == "/v1/responses" && isPermanentCredentialFailure(status) {
		return false
	}
	if upstreamcompat.IsOAuthLikeAccount(account) && status == http.StatusTooManyRequests {
		return false
	}
	return shouldRotateOnStatusCode(status, group)
}

func shouldRefreshOpenAIOAuthAfterAuthFailure(requestPath string, account config.Account, statusCode int) bool {
	return upstreamcompat.IsOpenAIOAuthAccount(account) && requestPath == "/v1/responses" && isPermanentCredentialFailure(statusCode) && credentialValue(account.Credential, "refresh_token") != ""
}

func shouldCooldownUpstreamFailure(requestPath string, err error) bool {
	if requestPath == "/v1/responses" && isTransientTLSFailure(err) {
		return false
	}
	return true
}

func isTransientTLSFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "tls: bad record mac") || strings.Contains(message, "tls: record header error") || strings.Contains(message, "tls: unexpected message") || strings.Contains(message, "malformed http") || strings.Contains(message, "unexpected eof")
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

func (h Handler) writeStream(w http.ResponseWriter, r *http.Request, resp *http.Response, accountID string, key config.GatewayKey, model string, taskType string) {
	if shouldPassthroughAnthropicMessagesSSE(r) {
		h.writeSSEStreamPassthrough(w, resp, accountID, key, model, taskType)
		return
	}
	if shouldPassthroughResponsesSSE(r) {
		h.writeSSEStreamPassthrough(w, resp, accountID, key, model, taskType)
		return
	}
	h.writeReencodedStream(w, resp, accountID, key, model, taskType)
}

func (h Handler) writeReencodedStream(w http.ResponseWriter, resp *http.Response, accountID string, key config.GatewayKey, model string, taskType string) {
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

func (h Handler) writeSSEStreamPassthrough(w http.ResponseWriter, resp *http.Response, accountID string, key config.GatewayKey, model string, taskType string) {
	copySafeHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", contentType(resp.Header.Get("Content-Type"), "text/event-stream"))
	if strings.TrimSpace(w.Header().Get("Cache-Control")) == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = copyStreamWithFlush(w, resp.Body, flusher)
		h.recordRequest(accountID, key, model, taskType, resp.StatusCode, false, "upstream returned HTTP "+http.StatusText(resp.StatusCode))
		return
	}
	if _, err := copyStreamWithFlush(w, resp.Body, flusher); err != nil {
		h.recordRequest(accountID, key, model, taskType, http.StatusBadGateway, false, "upstream stream passthrough failed")
		return
	}
	h.recordRequest(accountID, key, model, taskType, resp.StatusCode, true, "")
}

func shouldPassthroughAnthropicMessagesSSE(r *http.Request) bool {
	if r == nil || r.URL == nil || r.URL.Path != "/v1/messages" {
		return false
	}
	return true
}

func shouldPassthroughResponsesSSE(r *http.Request) bool {
	if r == nil || r.URL == nil || r.URL.Path != "/v1/responses" {
		return false
	}
	if headerContainsAnyFold(r.Header, "Originator", "codex-tui", "codex_cli_rs") {
		return true
	}
	if headerContainsAnyFold(r.Header, "User-Agent", "codex-tui", "codex_cli_rs") {
		return true
	}
	if headerContainsAnyFold(r.Header, "Originator", "roo-code", "cline") {
		return true
	}
	if headerContainsAnyFold(r.Header, "User-Agent", "roo-code", "cline") {
		return true
	}
	for key := range r.Header {
		if strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Stainless-") {
			return true
		}
	}
	return false
}

func headerContainsAnyFold(header http.Header, key string, needles ...string) bool {
	if header == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(header.Get(key)))
	if value == "" {
		return false
	}
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(strings.TrimSpace(needle))) {
			return true
		}
	}
	return false
}

func copyStreamWithFlush(dst io.Writer, src io.Reader, flusher http.Flusher) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			nw, writeErr := dst.Write(chunk)
			written += int64(nw)
			if flusher != nil {
				flusher.Flush()
			}
			if writeErr != nil {
				return written, writeErr
			}
			if nw != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

func (h Handler) persistOpenAICodexUsageSnapshot(account config.Account, headers http.Header) {
	if h.Store == nil || !upstreamcompat.IsOpenAIOAuthAccount(account) {
		return
	}
	snapshot := parseOpenAICodexRateLimitHeaders(headers)
	updates := buildOpenAICodexUsageMetadataUpdates(snapshot, time.Now().UTC())
	if len(updates) == 0 {
		return
	}
	if _, ok, err := h.Store.UpdateAccountMetadata(account.ID, config.AccountMetadataUpdate{Metadata: updates}); err != nil {
		if h.Logger != nil {
			h.Logger.Warn("openai_codex_usage_snapshot_persist_failed", slog.String("account_id", account.ID), slog.String("error", err.Error()))
		}
	} else if ok && h.Logger != nil {
		h.Logger.Info("openai_codex_usage_snapshot_persisted", slog.String("account_id", account.ID))
	}
}

func parseOpenAICodexRateLimitHeaders(headers http.Header) *openAICodexUsageSnapshot {
	if headers == nil {
		return nil
	}
	snapshot := &openAICodexUsageSnapshot{}
	hasData := false
	parseFloat := func(key string) *float64 {
		if v := strings.TrimSpace(headers.Get(key)); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return &f
			}
		}
		return nil
	}
	parseInt := func(key string) *int {
		if v := strings.TrimSpace(headers.Get(key)); v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				return &i
			}
		}
		return nil
	}
	if v := parseFloat("x-codex-primary-used-percent"); v != nil {
		snapshot.PrimaryUsedPercent = v
		hasData = true
	}
	if v := parseInt("x-codex-primary-reset-after-seconds"); v != nil {
		snapshot.PrimaryResetAfterSeconds = v
		hasData = true
	}
	if v := parseInt("x-codex-primary-window-minutes"); v != nil {
		snapshot.PrimaryWindowMinutes = v
		hasData = true
	}
	if v := parseFloat("x-codex-secondary-used-percent"); v != nil {
		snapshot.SecondaryUsedPercent = v
		hasData = true
	}
	if v := parseInt("x-codex-secondary-reset-after-seconds"); v != nil {
		snapshot.SecondaryResetAfterSeconds = v
		hasData = true
	}
	if v := parseInt("x-codex-secondary-window-minutes"); v != nil {
		snapshot.SecondaryWindowMinutes = v
		hasData = true
	}
	if v := parseFloat("x-codex-primary-over-secondary-limit-percent"); v != nil {
		snapshot.PrimaryOverSecondaryPercent = v
		hasData = true
	}
	if !hasData {
		return nil
	}
	snapshot.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return snapshot
}

func buildOpenAICodexUsageMetadataUpdates(snapshot *openAICodexUsageSnapshot, fallbackNow time.Time) map[string]any {
	if snapshot == nil {
		return nil
	}
	baseTime := codexSnapshotBaseTime(snapshot, fallbackNow)
	updates := map[string]any{"codex_usage_updated_at": baseTime.Format(time.RFC3339)}
	if snapshot.PrimaryUsedPercent != nil {
		updates["codex_primary_used_percent"] = *snapshot.PrimaryUsedPercent
	}
	if snapshot.PrimaryResetAfterSeconds != nil {
		updates["codex_primary_reset_after_seconds"] = *snapshot.PrimaryResetAfterSeconds
	}
	if snapshot.PrimaryWindowMinutes != nil {
		updates["codex_primary_window_minutes"] = *snapshot.PrimaryWindowMinutes
	}
	if snapshot.SecondaryUsedPercent != nil {
		updates["codex_secondary_used_percent"] = *snapshot.SecondaryUsedPercent
	}
	if snapshot.SecondaryResetAfterSeconds != nil {
		updates["codex_secondary_reset_after_seconds"] = *snapshot.SecondaryResetAfterSeconds
	}
	if snapshot.SecondaryWindowMinutes != nil {
		updates["codex_secondary_window_minutes"] = *snapshot.SecondaryWindowMinutes
	}
	if snapshot.PrimaryOverSecondaryPercent != nil {
		updates["codex_primary_over_secondary_percent"] = *snapshot.PrimaryOverSecondaryPercent
	}
	if normalized := snapshot.normalize(); normalized != nil {
		if normalized.Used5hPercent != nil {
			updates["codex_5h_used_percent"] = *normalized.Used5hPercent
		}
		if normalized.Reset5hSeconds != nil {
			updates["codex_5h_reset_after_seconds"] = *normalized.Reset5hSeconds
		}
		if normalized.Window5hMinutes != nil {
			updates["codex_5h_window_minutes"] = *normalized.Window5hMinutes
		}
		if normalized.Used7dPercent != nil {
			updates["codex_7d_used_percent"] = *normalized.Used7dPercent
		}
		if normalized.Reset7dSeconds != nil {
			updates["codex_7d_reset_after_seconds"] = *normalized.Reset7dSeconds
		}
		if normalized.Window7dMinutes != nil {
			updates["codex_7d_window_minutes"] = *normalized.Window7dMinutes
		}
		if reset5hAt := codexResetAtRFC3339(baseTime, normalized.Reset5hSeconds); reset5hAt != nil {
			updates["codex_5h_reset_at"] = *reset5hAt
		}
		if reset7dAt := codexResetAtRFC3339(baseTime, normalized.Reset7dSeconds); reset7dAt != nil {
			updates["codex_7d_reset_at"] = *reset7dAt
		}
	}
	return updates
}

func codexSnapshotBaseTime(snapshot *openAICodexUsageSnapshot, fallback time.Time) time.Time {
	if snapshot == nil || strings.TrimSpace(snapshot.UpdatedAt) == "" {
		return fallback
	}
	base, err := time.Parse(time.RFC3339, snapshot.UpdatedAt)
	if err != nil {
		return fallback
	}
	return base.UTC()
}

func codexResetAtRFC3339(base time.Time, resetAfterSeconds *int) *string {
	if resetAfterSeconds == nil {
		return nil
	}
	seconds := *resetAfterSeconds
	if seconds < 0 {
		seconds = 0
	}
	resetAt := base.UTC().Add(time.Duration(seconds) * time.Second).Format(time.RFC3339)
	return &resetAt
}

func (h Handler) cooldown(accountID string) {
	h.cooldownWithGroup(accountID, nil, 0)
}

func (h Handler) cooldownWithGroup(accountID string, group *config.Group, override time.Duration) {
	h.cooldownWithGroupReason(accountID, group, override, "upstream_error", 0)
}

func (h Handler) cooldownWithGroupReason(accountID string, group *config.Group, override time.Duration, reason string, statusCode int) {
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
		attrs := []any{"account_id", accountID, "duration_seconds", int(duration.Seconds()), "reason", reason}
		if statusCode > 0 {
			attrs = append(attrs, "status_code", statusCode)
		}
		if group != nil {
			attrs = append(attrs, "group_id", group.ID)
		}
		h.Logger.Warn("gateway upstream account cooldown", attrs...)
	}
}

func (h Handler) logNoEligibleAccount(requestPath string, model string, taskType string, key config.GatewayKey, decision routing.Decision, group *config.Group, excluded map[string]bool, attempt int, maxAttempts int, err error) {
	if h.Logger == nil {
		return
	}
	attrs := h.routingLogAttrs("gateway no eligible account", requestPath, model, taskType, key, decision, group, excluded, attempt, maxAttempts)
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	h.Logger.Warn("gateway no eligible account", attrs...)
}

func (h Handler) logGroupExhausted(requestPath string, model string, taskType string, key config.GatewayKey, decision routing.Decision, group *config.Group, excluded map[string]bool, maxAttempts int, message string) {
	if h.Logger == nil {
		return
	}
	attrs := h.routingLogAttrs("gateway group exhausted", requestPath, model, taskType, key, decision, group, excluded, maxAttempts, maxAttempts)
	attrs = append(attrs, "message", message)
	h.Logger.Warn("gateway group exhausted", attrs...)
}

func (h Handler) logUpstreamError(requestPath string, model string, taskType string, key config.GatewayKey, group *config.Group, accountID string, statusCode int, attempt int, maxAttempts int, rotateOnStatus bool, upstreamBody []byte, upstreamHeader http.Header) {
	if h.Logger == nil {
		return
	}
	attrs := []any{
		"path", requestPath,
		"model", model,
		"task_type", taskType,
		"gateway_key_id", key.ID,
		"gateway_key_ref", key.Preview,
		"account_id", accountID,
		"status_code", statusCode,
		"attempt", attempt,
		"max_attempts", maxAttempts,
		"rotate_on_status", rotateOnStatus,
	}
	if group != nil {
		attrs = append(attrs, "group_id", group.ID, "group_account_ids", append([]string(nil), group.AccountIDs...), "retry_on_errors", group.RotationPolicy.RetryOnErrors)
	}
	if h.DebugAPI {
		attrs = append(attrs, "upstream_response_header_keys", sortedHeaderKeys(upstreamHeader))
		if len(upstreamBody) > 0 {
			attrs = append(attrs,
				"upstream_response_body_bytes", len(upstreamBody),
				"upstream_response_body_sha256", sha256Hex(upstreamBody),
				"upstream_response_body_preview", scrubBodyPreview(upstreamBody),
			)
		}
	}
	h.Logger.Warn("gateway upstream error status", attrs...)
}

func (h Handler) routingLogAttrs(event string, requestPath string, model string, taskType string, key config.GatewayKey, decision routing.Decision, group *config.Group, excluded map[string]bool, attempt int, maxAttempts int) []any {
	attrs := []any{
		"event", event,
		"path", requestPath,
		"model", model,
		"task_type", taskType,
		"gateway_key_id", key.ID,
		"gateway_key_ref", key.Preview,
		"routing_mode", key.RoutingPolicy.Mode,
		"policy_group_ids", append([]string(nil), key.RoutingPolicy.GroupIDs...),
		"policy_tiers", append([]string(nil), key.RoutingPolicy.Tiers...),
		"policy_account_ids", append([]string(nil), key.RoutingPolicy.AccountIDs...),
		"decision_rule", decision.MatchedRule,
		"decision_prefer_tiers", append([]string(nil), decision.PreferTiers...),
		"decision_fallback_tiers", append([]string(nil), decision.FallbackTiers...),
		"excluded_account_ids", sortedExcludedIDs(excluded),
		"attempt", attempt,
		"max_attempts", maxAttempts,
	}
	if group != nil {
		attrs = append(attrs,
			"group_id", group.ID,
			"group_status", group.Status,
			"group_account_ids", append([]string(nil), group.AccountIDs...),
			"retry_on_errors", group.RotationPolicy.RetryOnErrors,
			"rotate_error_codes", append([]int(nil), group.RotationPolicy.RotateErrorCodes...),
			"cooldown_duration_seconds", group.RotationPolicy.CooldownDurationSeconds,
		)
	} else {
		attrs = append(attrs, "group_id", "")
	}
	if h.Pool != nil {
		attrs = append(attrs, "pool_accounts", poolAccountDebugSummaries(h.Pool.Snapshot().Accounts))
	}
	return attrs
}

func sortedExcludedIDs(excluded map[string]bool) []string {
	out := make([]string, 0, len(excluded))
	for id, isExcluded := range excluded {
		if isExcluded {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

type poolAccountDebugSummary struct {
	AccountID      string `json:"account_id"`
	Tier           string `json:"tier"`
	Status         string `json:"status"`
	CheckStatus    string `json:"check_status,omitempty"`
	QuotaStatus    string `json:"quota_status,omitempty"`
	CooldownUntil  string `json:"cooldown_until,omitempty"`
	ActiveConns    int    `json:"active_conns"`
	LastSelectedAt string `json:"last_selected_at,omitempty"`
}

func poolAccountDebugSummaries(accounts []accountpool.AccountState) []poolAccountDebugSummary {
	out := make([]poolAccountDebugSummary, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, poolAccountDebugSummary{
			AccountID:      account.AccountID,
			Tier:           account.Tier,
			Status:         account.Status,
			CheckStatus:    account.Check.Status,
			QuotaStatus:    string(account.Quota.Status),
			CooldownUntil:  account.CooldownUntil,
			ActiveConns:    account.ActiveConns,
			LastSelectedAt: account.LastSelectedAt,
		})
	}
	return out
}

func (h Handler) recordFailureAndMaybeDisable(account config.Account, requestPath string, statusCode int) {
	accountID := account.ID
	if h.Pool == nil || h.Store == nil || !isPermanentCredentialFailure(statusCode) || !shouldAutoDisable(requestPath, account, statusCode) {
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

func shouldAutoDisable(requestPath string, account config.Account, statusCode int) bool {
	if requestPath == "/v1/responses" && statusCode == http.StatusNotFound {
		return false
	}
	if upstreamcompat.IsOpenAIOAuthAccount(account) && requestPath == "/v1/responses" && isPermanentCredentialFailure(statusCode) {
		return false
	}
	return true
}

func shouldCooldownStatus(requestPath string, account config.Account, statusCode int) bool {
	if requestPath == "/v1/responses" && statusCode == http.StatusNotFound {
		return false
	}
	if upstreamcompat.IsOAuthLikeAccount(account) && statusCode == http.StatusTooManyRequests {
		return false
	}
	return true
}

func credentialValue(credential string, key string) string {
	for _, part := range strings.FieldsFunc(credential, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		name, value, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(name) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func tokenNotExpired(credential string, skew time.Duration) bool {
	expiresAt := credentialValue(credential, "expires_at")
	if expiresAt == "" {
		return true
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true
	}
	return parsed.After(time.Now().UTC().Add(skew))
}

func mergeCredential(credential string, updates map[string]string) string {
	values := map[string]string{}
	keys := make([]string, 0)
	for _, part := range strings.FieldsFunc(credential, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, exists := values[key]; !exists {
			keys = append(keys, key)
		}
		values[key] = strings.TrimSpace(value)
	}
	for key, value := range updates {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if _, exists := values[key]; !exists {
			keys = append(keys, key)
		}
		values[key] = value
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := strings.TrimSpace(values[key]); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return strings.Join(parts, ";")
}

func sanitizeCredentialError(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeCredentialText(err.Error())
}

func sanitizeCredentialText(text string) string {
	for _, marker := range []string{"Bearer ", "sk-", "access_token=", "refresh_token=", "id_token="} {
		if strings.Contains(text, marker) {
			return "credential operation failed"
		}
	}
	return text
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

func normalizeGatewayRequestPath(r *http.Request) {
	if r == nil || r.URL == nil {
		return
	}
	switch r.URL.Path {
	case "/v1/v1/models":
		r.URL.Path = "/v1/models"
	case "/v1/v1/chat/completions":
		r.URL.Path = "/v1/chat/completions"
	case "/v1/v1/responses":
		r.URL.Path = "/v1/responses"
	case "/v1/v1/messages":
		r.URL.Path = "/v1/messages"
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
