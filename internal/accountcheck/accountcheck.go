package accountcheck

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/proxyclient"
	"github.com/0xForce-Network/simple-sub2api/internal/upstreamcompat"
)

type Result struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	CheckedAt string `json:"checked_at"`
	ProxyID   string `json:"proxy_id,omitempty"`
}

type Checker struct {
	Timeout time.Duration
}

func New(cfg config.ProbeConfig) Checker {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return Checker{Timeout: timeout}
}

func (c Checker) Check(ctx context.Context, cfg config.Config, account config.Account) Result {
	result := Result{AccountID: account.ID, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if !account.Enabled {
		result.Status = "disabled"
		return result
	}
	if strings.TrimSpace(account.Credential) == "" {
		result.Status = "error"
		result.Message = "credential is required"
		return result
	}
	specs := proxyclient.SpecsFromConfig(cfg)
	spec, hasProxy, err := proxyclient.Resolve(account, specs)
	if err != nil {
		result.Status = "error"
		result.Message = sanitize(err)
		return result
	}
	if hasProxy {
		result.ProxyID = spec.ID
	}
	if upstreamcompat.IsOpenAIOAuthAccount(account) {
		if openAIOAuthCredentialPresent(account.Credential) {
			result.Status = "healthy"
			result.Message = "oauth credential validation passed"
			return result
		}
		result.Status = "error"
		result.Message = "openai oauth access_token or refresh_token is required"
		return result
	}
	if strings.TrimSpace(account.BaseURL) == "" {
		result.Status = "healthy"
		result.Message = "static credential validation passed"
		return result
	}
	client, err := proxyclient.HTTPClient(spec, c.Timeout)
	if err != nil {
		result.Status = "error"
		result.Message = sanitize(err)
		return result
	}
	probeURL, err := upstreamcompat.OpenAIAPIURL(account.BaseURL, "/v1/models")
	if err != nil {
		result.Status = "error"
		result.Message = "probe URL is invalid"
		return result
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		result.Status = "error"
		result.Message = "probe URL is invalid"
		return result
	}
	if apiKey := apiKeyFromCredential(account.Credential); apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(request)
	if err != nil {
		result.Status = "error"
		result.Message = sanitize(err)
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Status = "healthy"
		result.Message = "probe succeeded"
		return result
	}
	result.Status = "error"
	result.Message = fmt.Sprintf("probe returned HTTP %d", resp.StatusCode)
	return result
}

func CheckAll(ctx context.Context, cfg config.Config) []Result {
	checker := New(cfg.Probe)
	out := make([]Result, 0, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		out = append(out, checker.Check(ctx, cfg, account))
	}
	return out
}

func apiKeyFromCredential(credential string) string {
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

func openAIOAuthCredentialPresent(credential string) bool {
	for _, part := range strings.FieldsFunc(credential, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		key, value, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		switch strings.TrimSpace(key) {
		case "access_token", "refresh_token":
			return true
		}
	}
	return false
}

func sanitize(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if errors.Is(err, context.DeadlineExceeded) {
		return "probe timed out"
	}
	for _, marker := range []string{"api_key=", "sk-", "sess-", "refresh_token="} {
		if strings.Contains(message, marker) {
			return "probe failed"
		}
	}
	return message
}
