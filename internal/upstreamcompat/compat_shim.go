package upstreamcompat

import (
	"net/http"
	"strings"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

func BuildUpstreamRequest(base *http.Request, account config.Account, body []byte) (*http.Request, error) {
	baseURL := strings.TrimRight(account.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	request, err := http.NewRequestWithContext(base.Context(), http.MethodPost, baseURL+"/v1/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", base.Header.Get("Accept"))
	if key := APIKeyFromCredential(account.Credential); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	return request, nil
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
