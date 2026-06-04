package upstreamcompat

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

const defaultUserAgent = "simple-sub2api-gateway/1.0"
const codexCLIUserAgent = "codex_cli_rs/0.125.0"
const chatGPTCodexResponsesURL = "https://chatgpt.com/backend-api/codex/responses"

const claudeOAuthBeta = "oauth-2025-04-20"
const claudeCodeBeta = "claude-code-20250219"
const claudeInterleavedThinkingBeta = "interleaved-thinking-2025-05-14"
const claudePromptCachingScopeBeta = "prompt-caching-scope-2026-01-05"
const claudeEffortBeta = "effort-2025-11-24"
const claudeContextManagementBeta = "context-management-2025-06-27"
const claudeContext1MBeta = "context-1m-2025-08-07"
const claudeRedactThinkingBeta = "redact-thinking-2026-02-12"
const claudeDefaultUserAgent = "claude-cli/2.1.126 (external, cli)"
const claudeNewMetadataFormatMinVersion = "2.1.78"

var metadataUserIDLegacyPattern = regexp.MustCompile(`^user_([a-fA-F0-9]{64})_account_([a-fA-F0-9-]*)_session_([a-fA-F0-9-]{36})$`)

var claudeCodeDefaultHeaders = map[string]string{
	"User-Agent":                                claudeDefaultUserAgent,
	"X-Stainless-Lang":                          "js",
	"X-Stainless-Package-Version":               "0.81.0",
	"X-Stainless-OS":                            "Linux",
	"X-Stainless-Arch":                          "x64",
	"X-Stainless-Runtime":                       "node",
	"X-Stainless-Runtime-Version":               "v24.3.0",
	"X-Stainless-Retry-Count":                   "0",
	"X-Stainless-Timeout":                       "600",
	"X-App":                                     "cli",
	"Anthropic-Dangerous-Direct-Browser-Access": "true",
}

var openAIChatGPTInternalUnsupportedFields = []string{
	"user",
	"metadata",
	"prompt_cache_retention",
	"safety_identifier",
	"stream_options",
}

var openAICodexOAuthUnsupportedFields = append([]string{
	"max_output_tokens",
	"max_completion_tokens",
	"temperature",
	"top_p",
	"frequency_penalty",
	"presence_penalty",
}, openAIChatGPTInternalUnsupportedFields...)

var upstreamHeaderAllowlist = map[string]struct{}{
	"Accept":          {},
	"Accept-Language": {},
	"Anthropic-Beta":  {},
	"Anthropic-Dangerous-Direct-Browser-Access": {},
	"Anthropic-Version":                         {},
	"Cache-Control":                             {},
	"Conversation-Id":                           {},
	"Content-Type":                              {},
	"Conversation_id":                           {},
	"Idempotency-Key":                           {},
	"Openai-Beta":                               {},
	"Openai-Organization":                       {},
	"Openai-Project":                            {},
	"Session_id":                                {},
	"Session-Id":                                {},
	"User-Agent":                                {},
	"Version":                                   {},
	"X-Stainless-Arch":                          {},
	"X-Stainless-Async":                         {},
	"X-Stainless-Lang":                          {},
	"X-Stainless-Os":                            {},
	"X-Stainless-Package-Version":               {},
	"X-Stainless-Retry-Count":                   {},
	"X-Stainless-Runtime":                       {},
	"X-Stainless-Runtime-Version":               {},
	"X-Stainless-Timeout":                       {},
	"X-App":                                     {},
	"X-Client-Request-Id":                       {},
	"X-Claude-Code-Session-Id":                  {},
}

func BuildUpstreamRequest(base *http.Request, account config.Account, body []byte) (*http.Request, error) {
	useOfficialResponsesAPI := shouldUseOfficialResponsesAPIForOAuth(base, account)
	requestURL, err := AccountUpstreamURL(account, base.URL.Path, useOfficialResponsesAPI)
	if err != nil {
		return nil, err
	}
	mimicClaudeCode := false
	if isAnthropicMessagesRequest(base, account) && IsAnthropicOAuthAccount(account) {
		mimicClaudeCode = !isClaudeCodeClientRequest(base.Header, body)
		billingUserAgent := strings.TrimSpace(base.Header.Get("User-Agent"))
		if mimicClaudeCode || extractClaudeCodeVersion(billingUserAgent) == "" {
			billingUserAgent = claudeDefaultUserAgent
		}
		body = normalizeAnthropicOAuthMessagesBody(body, account, base.Header, billingUserAgent)
		if mimicClaudeCode {
			body = ensureAnthropicBillingHeader(body, billingUserAgent)
		}
		body = normalizeAnthropicBillingHeader(body, billingUserAgent)
	} else {
		body = normalizeAnthropicBillingHeader(body, base.Header.Get("User-Agent"))
	}
	if IsOpenAIOAuthAccount(account) && !useOfficialResponsesAPI {
		body = normalizeOpenAIOAuthResponsesBody(base, body)
	}
	request, err := http.NewRequestWithContext(base.Context(), http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyUpstreamProtocolHeaders(request.Header, base.Header)
	if strings.TrimSpace(request.Header.Get("Content-Type")) == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(request.Header.Get("Accept")) == "" {
		request.Header.Set("Accept", "application/json")
	}
	if strings.TrimSpace(request.Header.Get("User-Agent")) == "" {
		request.Header.Set("User-Agent", defaultUserAgent)
	}
	if key := APIKeyFromCredential(account.Credential); key != "" {
		if IsAnthropicAccount(account) && strings.TrimSpace(base.URL.Path) == "/v1/messages" {
			applyAnthropicMessagesAuth(request, account, key)
		} else {
			request.Header.Set("Authorization", "Bearer "+key)
		}
	}
	if isAnthropicMessagesRequest(base, account) {
		applyAnthropicMessagesHeaders(request, account, body, mimicClaudeCode)
	}
	if IsOpenAIOAuthAccount(account) && !useOfficialResponsesAPI {
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		applyOpenAIOAuthCodexHeaders(request, base, account, body)
	}
	return request, nil
}

func isAnthropicMessagesRequest(base *http.Request, account config.Account) bool {
	return IsAnthropicAccount(account) && base != nil && base.URL != nil && strings.TrimSpace(base.URL.Path) == "/v1/messages"
}

func AccountUpstreamURL(account config.Account, endpointPath string, useOfficialResponsesAPI bool) (string, error) {
	if IsOpenAIOAuthAccount(account) {
		if useOfficialResponsesAPI {
			return OpenAIAPIURL(account.BaseURL, endpointPath)
		}
		return OpenAIOAuthCodexURL(account.BaseURL, endpointPath)
	}
	if IsAnthropicAccount(account) && strings.TrimSpace(endpointPath) == "/v1/messages" {
		return AnthropicMessagesAPIURL(account.BaseURL)
	}
	return OpenAIAPIURL(account.BaseURL, endpointPath)
}

func AnthropicMessagesAPIURL(baseURL string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.anthropic.com"
	}
	upstreamURL, err := OpenAIAPIURL(baseURL, "/v1/messages")
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(upstreamURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	if strings.TrimSpace(query.Get("beta")) == "" {
		query.Set("beta", "true")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func OpenAIOAuthCodexURL(baseURL string, endpointPath string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" || strings.Contains(trimmed, "api.openai.com") {
		trimmed = chatGPTCodexResponsesURL
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if strings.Contains(parsed.Host, "api.openai.com") || strings.TrimSpace(parsed.Host) == "" {
		parsed, err = url.Parse(chatGPTCodexResponsesURL)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimRight(parsed.EscapedPath(), "/") == "" {
		parsed.Path = "/backend-api/codex/responses"
		parsed.RawPath = ""
	}
	return parsed.String(), nil
}

func IsOpenAIOAuthAccount(account config.Account) bool {
	return config.AccountPlatform(account) == "openai" && strings.TrimSpace(account.Type) == "oauth"
}

func IsAnthropicAccount(account config.Account) bool {
	return config.AccountPlatform(account) == "anthropic"
}

func IsAnthropicOAuthAccount(account config.Account) bool {
	accountType := strings.TrimSpace(account.Type)
	return IsAnthropicAccount(account) && (accountType == "oauth" || accountType == "setup-token")
}

func IsGeminiOAuthAccount(account config.Account) bool {
	accountType := strings.TrimSpace(account.Type)
	return config.AccountPlatform(account) == "gemini" && (accountType == "oauth" || accountType == "setup-token")
}

func IsAntigravityOAuthAccount(account config.Account) bool {
	accountType := strings.TrimSpace(account.Type)
	return config.AccountPlatform(account) == "antigravity" && (accountType == "oauth" || accountType == "setup-token")
}

func IsOAuthLikeAccount(account config.Account) bool {
	return IsOpenAIOAuthAccount(account) || IsAnthropicOAuthAccount(account) || IsGeminiOAuthAccount(account) || IsAntigravityOAuthAccount(account)
}

func applyAnthropicMessagesAuth(request *http.Request, account config.Account, key string) {
	request.Header.Del("Authorization")
	request.Header.Del("X-Api-Key")
	if IsAnthropicOAuthAccount(account) {
		request.Header.Set("Authorization", "Bearer "+key)
		return
	}
	request.Header.Set("X-Api-Key", key)
}

func applyAnthropicMessagesHeaders(request *http.Request, account config.Account, body []byte, mimicClaudeCode bool) {
	if !IsAnthropicOAuthAccount(account) {
		return
	}
	clientBeta := request.Header.Get("Anthropic-Beta")
	if !mimicClaudeCode {
		applyClaudeOAuthHeaderDefaults(request)
		request.Header.Set("Anthropic-Beta", claudeOAuthBetaForClient(modelFromBody(body), clientBeta))
		syncClaudeCodeSessionHeader(request, body)
		return
	}
	request.Header = http.Header{}
	request.Header.Set("Content-Type", "application/json")
	applyClaudeCodeMimicHeaders(request, bodyRequestsStream(body))
	request.Header.Set("Anthropic-Version", "2023-06-01")
	request.Header.Set("Anthropic-Beta", mergeAnthropicBeta("", requiredClaudeOAuthBetas(modelFromBody(body), clientBeta)...))
	if key := APIKeyFromCredential(account.Credential); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	syncClaudeCodeSessionHeader(request, body)
}

func claudeOAuthBetaForClient(model string, clientBeta string) string {
	clientBeta = strings.TrimSpace(clientBeta)
	if clientBeta != "" {
		if strings.Contains(clientBeta, claudeOAuthBeta) {
			return clientBeta
		}
		parts := strings.Split(clientBeta, ",")
		for i, part := range parts {
			if strings.TrimSpace(part) == claudeCodeBeta {
				out := make([]string, 0, len(parts)+1)
				out = append(out, parts[:i+1]...)
				out = append(out, claudeOAuthBeta)
				out = append(out, parts[i+1:]...)
				return strings.Join(out, ",")
			}
		}
		return claudeOAuthBeta + "," + clientBeta
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(model)), "haiku") {
		return claudeOAuthBeta + "," + claudeInterleavedThinkingBeta
	}
	return claudeCodeBeta + "," + claudeOAuthBeta + "," + claudeInterleavedThinkingBeta
}

func applyClaudeOAuthHeaderDefaults(request *http.Request) {
	if strings.TrimSpace(request.Header.Get("Accept")) == "" {
		request.Header.Set("Accept", "application/json")
	}
	if strings.TrimSpace(request.Header.Get("Anthropic-Version")) == "" {
		request.Header.Set("Anthropic-Version", "2023-06-01")
	}
	for key, value := range claudeCodeDefaultHeaders {
		if strings.TrimSpace(value) != "" && strings.TrimSpace(request.Header.Get(key)) == "" {
			request.Header.Set(key, value)
		}
	}
}

func applyClaudeCodeMimicHeaders(request *http.Request, stream bool) {
	for key, value := range claudeCodeDefaultHeaders {
		if strings.TrimSpace(value) != "" {
			request.Header.Set(key, value)
		}
	}
	request.Header.Set("Accept", "application/json")
	if stream {
		request.Header.Set("X-Stainless-Helper-Method", "stream")
	}
	if strings.TrimSpace(request.Header.Get("X-Client-Request-Id")) == "" {
		request.Header.Set("X-Client-Request-Id", randomUUID())
	}
}

func requiredClaudeOAuthBetas(model string, incoming string) []string {
	out := []string{}
	add := func(token string) {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			return
		}
		for _, existing := range out {
			if strings.EqualFold(existing, trimmed) {
				return
			}
		}
		out = append(out, trimmed)
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(model)), "haiku") {
		add(claudeOAuthBeta)
		add(claudeInterleavedThinkingBeta)
	} else {
		for _, token := range []string{
			claudeCodeBeta,
			claudeOAuthBeta,
			claudeContext1MBeta,
			claudeInterleavedThinkingBeta,
			claudeRedactThinkingBeta,
			claudePromptCachingScopeBeta,
			claudeEffortBeta,
			claudeContextManagementBeta,
		} {
			add(token)
		}
	}
	for _, token := range strings.Split(incoming, ",") {
		add(token)
	}
	return out
}

func mergeAnthropicBeta(existing string, required ...string) string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, token := range append(strings.Split(existing, ","), required...) {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	return strings.Join(out, ",")
}

func normalizeAnthropicOAuthMessagesBody(body []byte, account config.Account, headers http.Header, userAgent string) []byte {
	if len(body) == 0 {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		payload["metadata"] = metadata
	}
	originalUserID, _ := metadata["user_id"].(string)
	parsed := parseMetadataUserID(originalUserID)
	accountUUID := firstNonEmpty(credentialValue(account.Credential, "account_uuid"), credentialValue(account.Credential, "org_uuid"), metadataString(account.Metadata, "account_uuid"), metadataString(account.Metadata, "org_uuid"))
	if accountUUID == "" && parsed != nil {
		accountUUID = parsed.AccountUUID
	}
	sessionID := ""
	if parsed != nil {
		sessionID = parsed.SessionID
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(headers.Get("X-Claude-Code-Session-Id"))
	}
	if sessionID == "" {
		sessionID = deterministicUUID(account.ID + "::" + string(body))
	}
	deviceID := deterministicDeviceID(account.ID, accountUUID)
	if parsed != nil && len(parsed.DeviceID) == 64 {
		deviceID = deterministicDeviceID(account.ID, parsed.DeviceID)
	}
	newSession := deterministicUUID(account.ID + "::" + sessionID)
	metadata["user_id"] = formatMetadataUserID(deviceID, accountUUID, newSession, extractClaudeCodeVersion(userAgent))
	normalized, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return normalized
}

func isClaudeCodeClientRequest(headers http.Header, body []byte) bool {
	if headers == nil || extractClaudeCodeVersion(headers.Get("User-Agent")) == "" {
		return false
	}
	return parseMetadataUserID(metadataUserIDFromBody(body)) != nil
}

type parsedMetadataUserID struct {
	DeviceID    string
	AccountUUID string
	SessionID   string
}

func parseMetadataUserID(raw string) *parsedMetadataUserID {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "{") {
		var payload struct {
			DeviceID    string `json:"device_id"`
			AccountUUID string `json:"account_uuid"`
			SessionID   string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil || strings.TrimSpace(payload.DeviceID) == "" || strings.TrimSpace(payload.SessionID) == "" {
			return nil
		}
		return &parsedMetadataUserID{DeviceID: payload.DeviceID, AccountUUID: payload.AccountUUID, SessionID: payload.SessionID}
	}
	matches := metadataUserIDLegacyPattern.FindStringSubmatch(raw)
	if matches == nil {
		return nil
	}
	return &parsedMetadataUserID{DeviceID: matches[1], AccountUUID: matches[2], SessionID: matches[3]}
}

func formatMetadataUserID(deviceID, accountUUID, sessionID, version string) string {
	if compareVersions(version, claudeNewMetadataFormatMinVersion) >= 0 {
		payload := struct {
			DeviceID    string `json:"device_id"`
			AccountUUID string `json:"account_uuid"`
			SessionID   string `json:"session_id"`
		}{DeviceID: deviceID, AccountUUID: accountUUID, SessionID: sessionID}
		encoded, _ := json.Marshal(payload)
		return string(encoded)
	}
	return "user_" + deviceID + "_account_" + accountUUID + "_session_" + sessionID
}

func compareVersions(a, b string) int {
	ap := parseSemver(a)
	bp := parseSemver(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}

func parseSemver(value string) [3]int {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(value), "v"), ".")
	out := [3]int{}
	for i := 0; i < len(parts) && i < 3; i++ {
		if parsed, err := strconv.Atoi(parts[i]); err == nil {
			out[i] = parsed
		}
	}
	return out
}

func syncClaudeCodeSessionHeader(request *http.Request, body []byte) {
	uid := metadataUserIDFromBody(body)
	if uid == "" {
		return
	}
	parsed := parseMetadataUserID(uid)
	if parsed == nil || parsed.SessionID == "" {
		return
	}
	request.Header.Set("X-Claude-Code-Session-Id", parsed.SessionID)
}

func metadataUserIDFromBody(body []byte) string {
	var payload struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Metadata == nil {
		return ""
	}
	uid, _ := payload.Metadata["user_id"].(string)
	return strings.TrimSpace(uid)
}

func modelFromBody(body []byte) string {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Model)
}

func deterministicDeviceID(accountID string, extra string) string {
	sum := sha256.Sum256([]byte("simple-sub2api::claude-device::" + strings.TrimSpace(accountID) + "::" + strings.TrimSpace(extra)))
	return hex.EncodeToString(sum[:])
}

func deterministicUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	buf := append([]byte(nil), sum[:16]...)
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func randomUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return deterministicUUID(err.Error())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func applyOpenAIOAuthCodexHeaders(request *http.Request, base *http.Request, account config.Account, body []byte) {
	request.Host = "chatgpt.com"
	if accountID := credentialValue(account.Credential, "chatgpt_account_id"); accountID != "" {
		request.Header.Set("chatgpt-account-id", accountID)
	}
	if strings.TrimSpace(request.Header.Get("OpenAI-Beta")) == "" {
		request.Header.Set("OpenAI-Beta", "responses=experimental")
	}
	if strings.TrimSpace(request.Header.Get("originator")) == "" {
		request.Header.Set("originator", "codex_cli_rs")
	}
	if strings.TrimSpace(request.Header.Get("Version")) == "" {
		request.Header.Set("Version", "0.125.0")
	}
	if !isCodexCLIUserAgent(request.Header.Get("User-Agent")) {
		request.Header.Set("User-Agent", codexCLIUserAgent)
	}
	if shouldSetCodexAcceptHeader(request.Header.Get("Accept")) {
		request.Header.Set("Accept", codexAcceptHeader(base, body))
	}
	applyIsolatedCodexSessionHeaders(request, base, body)
}

func codexAcceptHeader(base *http.Request, body []byte) string {
	if bodyRequestsStream(body) {
		return "text/event-stream"
	}
	return "application/json"
}

func shouldSetCodexAcceptHeader(value string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return true
	}
	return trimmed == "application/json" || trimmed == "*/*"
}

func bodyRequestsStream(body []byte) bool {
	text := string(body)
	return strings.Contains(text, `"stream"`) && strings.Contains(text, "true")
}

func shouldUseOfficialResponsesAPIForOAuth(base *http.Request, account config.Account) bool {
	if !IsOpenAIOAuthAccount(account) || base == nil || base.URL == nil || strings.TrimSpace(base.URL.Path) != "/v1/responses" {
		return false
	}
	if wantsCodexInternalResponses(base, account) {
		return false
	}
	return false
}

func wantsCodexInternalResponses(base *http.Request, account config.Account) bool {
	if forcesCodexInternalResponses(account) {
		return true
	}
	if isCodexOfficialClientByHeaders(base.Header.Get("User-Agent"), base.Header.Get("Originator")) {
		return true
	}
	if isOpenAIResponsesOAuthScopeFailureProneClient(base) {
		return true
	}
	return false
}

func isOpenAIResponsesOAuthScopeFailureProneClient(base *http.Request) bool {
	if base == nil {
		return false
	}
	if headerContainsAnyFold(base.Header, "Originator", "roo-code", "cline") {
		return true
	}
	if headerContainsAnyFold(base.Header, "User-Agent", "roo-code", "cline") {
		return true
	}
	return false
}

func forcesCodexInternalResponses(account config.Account) bool {
	if credentialValue(account.Credential, "force_codex_responses") == "true" {
		return true
	}
	if credentialValue(account.Credential, "force_codex_responses") == "1" {
		return true
	}
	if credentialValue(account.Credential, "force_codex_internal") == "true" {
		return true
	}
	if credentialValue(account.Credential, "force_codex_internal") == "1" {
		return true
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

func normalizeOpenAIOAuthResponsesBody(base *http.Request, body []byte) []byte {
	if base == nil || base.URL == nil || strings.TrimSpace(base.URL.Path) != "/v1/responses" || len(body) == 0 {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	modified := false
	for _, key := range openAICodexOAuthUnsupportedFields {
		if _, ok := payload[key]; ok {
			delete(payload, key)
			modified = true
		}
	}
	if store, ok := payload["store"].(bool); !ok || store {
		payload["store"] = false
		modified = true
	}
	if stream, ok := payload["stream"].(bool); !ok || !stream {
		payload["stream"] = true
		modified = true
	}
	if !modified {
		return body
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return normalized
}

func applyIsolatedCodexSessionHeaders(request *http.Request, base *http.Request, body []byte) {
	seed := strings.TrimSpace(request.Header.Get("session_id"))
	if seed == "" && base != nil {
		seed = strings.TrimSpace(base.Header.Get("session_id"))
	}
	if seed == "" {
		seed = strings.TrimSpace(request.Header.Get("conversation_id"))
	}
	if seed == "" && base != nil {
		seed = strings.TrimSpace(base.Header.Get("conversation_id"))
	}
	if seed == "" {
		seed = promptCacheKey(body)
	}
	if seed == "" && base != nil {
		seed = base.Header.Get("Authorization")
	}
	if seed == "" {
		seed = string(body)
	}
	if seed == "" {
		return
	}
	isolated := isolateSessionID(seed)
	request.Header.Set("session_id", isolated)
	if strings.TrimSpace(request.Header.Get("conversation_id")) == "" {
		request.Header.Set("conversation_id", isolated)
	} else {
		request.Header.Set("conversation_id", isolateSessionID(request.Header.Get("conversation_id")))
	}
}

func promptCacheKey(body []byte) string {
	text := string(body)
	marker := `"prompt_cache_key"`
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(marker):]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[colon+1:])
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func isolateSessionID(seed string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(seed)))
	return "simple-" + hex.EncodeToString(sum[:16])
}

func isCodexCLIUserAgent(userAgent string) bool {
	return matchesCodexClientPrefixes(userAgent, []string{
		"codex_cli_rs/",
		"codex_vscode/",
		"codex_app/",
		"codex_chatgpt_desktop/",
		"codex_atlas/",
		"codex_exec/",
		"codex_sdk_ts/",
		"codex ",
		"codex-cli",
		"codex-tui",
	})
}

func isCodexOfficialClientByHeaders(userAgent string, originator string) bool {
	return isCodexCLIUserAgent(userAgent) || isCodexOfficialClientOriginator(originator)
}

func isCodexOfficialClientOriginator(originator string) bool {
	return matchesCodexClientPrefixes(originator, []string{"codex_", "codex "})
}

func matchesCodexClientPrefixes(value string, prefixes []string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}
	for _, prefix := range prefixes {
		normalized := strings.ToLower(strings.TrimSpace(prefix))
		if normalized == "" {
			continue
		}
		if strings.HasPrefix(lower, normalized) || strings.Contains(lower, normalized) {
			return true
		}
	}
	return false
}

func copyUpstreamProtocolHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if shouldDropUpstreamHeader(canonical) {
			continue
		}
		if _, ok := upstreamHeaderAllowlist[canonical]; !ok {
			continue
		}
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				dst.Add(canonical, value)
			}
		}
	}
}

func shouldDropUpstreamHeader(canonical string) bool {
	if strings.HasPrefix(canonical, "X-Simple-Sub2api-") || strings.HasPrefix(canonical, "X-Simple-Sub2API-") {
		return true
	}
	switch canonical {
	case "Authorization", "Connection", "Content-Length", "Cookie", "Host", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func OpenAIAPIURL(baseURL string, endpointPath string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		trimmed = "https://api.openai.com"
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "/v1" {
		path = ""
	}
	parsed.Path = path + "/" + strings.TrimLeft(endpointPath, "/")
	parsed.RawPath = ""
	return parsed.String(), nil
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

func APIKeyFromCredential(credential string) string {
	for _, part := range strings.FieldsFunc(credential, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		key, value, ok := strings.Cut(part, "=")
		if ok && (strings.TrimSpace(key) == "api_key" || strings.TrimSpace(key) == "access_token") {
			return strings.TrimSpace(value)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(credential), "sk-") {
		return strings.TrimSpace(credential)
	}
	return ""
}
