package upstreamcompat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

const defaultUserAgent = "simple-sub2api-gateway/1.0"
const codexCLIUserAgent = "codex_cli_rs/0.125.0"
const chatGPTCodexResponsesURL = "https://chatgpt.com/backend-api/codex/responses"

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
}

func BuildUpstreamRequest(base *http.Request, account config.Account, body []byte) (*http.Request, error) {
	requestURL, err := AccountUpstreamURL(account, base.URL.Path)
	if err != nil {
		return nil, err
	}
	body = normalizeAnthropicBillingHeader(body, base.Header.Get("User-Agent"))
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
		request.Header.Set("Authorization", "Bearer "+key)
	}
	if IsOpenAIOAuthAccount(account) {
		applyOpenAIOAuthCodexHeaders(request, base, account, body)
	}
	return request, nil
}

func AccountUpstreamURL(account config.Account, endpointPath string) (string, error) {
	if IsOpenAIOAuthAccount(account) {
		return OpenAIOAuthCodexURL(account.BaseURL, endpointPath)
	}
	return OpenAIAPIURL(account.BaseURL, endpointPath)
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
	if strings.TrimSpace(request.Header.Get("Accept")) == "" || strings.EqualFold(strings.TrimSpace(request.Header.Get("Accept")), "application/json") {
		request.Header.Set("Accept", codexAcceptHeader(base, body))
	}
	applyIsolatedCodexSessionHeaders(request, base, body)
}

func codexAcceptHeader(base *http.Request, body []byte) string {
	if base != nil && strings.TrimSpace(base.URL.Path) == "/v1/responses" {
		return "application/json"
	}
	if strings.Contains(string(body), `"stream"`) && strings.Contains(string(body), "true") {
		return "text/event-stream"
	}
	return "application/json"
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
	lower := strings.ToLower(strings.TrimSpace(userAgent))
	return strings.Contains(lower, "codex_cli_rs") || strings.Contains(lower, "codex-cli")
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
