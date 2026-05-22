package upstreamcompat

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

const defaultUserAgent = "simple-sub2api-gateway/1.0"

var upstreamHeaderAllowlist = map[string]struct{}{
	"Accept":          {},
	"Accept-Language": {},
	"Anthropic-Beta":  {},
	"Anthropic-Dangerous-Direct-Browser-Access": {},
	"Anthropic-Version":                         {},
	"Cache-Control":                             {},
	"Content-Type":                              {},
	"Idempotency-Key":                           {},
	"Openai-Beta":                               {},
	"Openai-Organization":                       {},
	"Openai-Project":                            {},
	"User-Agent":                                {},
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
	requestURL, err := OpenAIAPIURL(account.BaseURL, "/v1/chat/completions")
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
	return request, nil
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

func APIKeyFromCredential(credential string) string {
	for _, part := range strings.FieldsFunc(credential, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		key, value, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(key) == "api_key" {
			return strings.TrimSpace(value)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(credential), "sk-") {
		return strings.TrimSpace(credential)
	}
	return ""
}
