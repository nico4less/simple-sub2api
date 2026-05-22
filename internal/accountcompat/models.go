package accountcompat

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

const (
	DataType       = "sub2api-data"
	LegacyDataType = "sub2api-bundle"
	DataVersion    = 1

	StatusActive = "active"

	PlatformAnthropic   = "anthropic"
	PlatformOpenAI      = "openai"
	PlatformGemini      = "gemini"
	PlatformAntigravity = "antigravity"

	AccountTypeOAuth      = "oauth"
	AccountTypeSetupToken = "setup-token"
	AccountTypeAPIKey     = "apikey"
	AccountTypeUpstream   = "upstream"
)

type DataPayload struct {
	Type       string        `json:"type,omitempty"`
	Version    int           `json:"version,omitempty"`
	ExportedAt string        `json:"exported_at"`
	Proxies    []DataProxy   `json:"proxies"`
	Accounts   []DataAccount `json:"accounts"`
}

type DataProxy struct {
	ProxyKey string `json:"proxy_key"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Status   string `json:"status"`
}

type DataAccount struct {
	Name               string         `json:"name"`
	Notes              *string        `json:"notes,omitempty"`
	Platform           string         `json:"platform"`
	Type               string         `json:"type"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra,omitempty"`
	ProxyKey           *string        `json:"proxy_key,omitempty"`
	Concurrency        int            `json:"concurrency"`
	Priority           int            `json:"priority"`
	RateMultiplier     *float64       `json:"rate_multiplier,omitempty"`
	ExpiresAt          *int64         `json:"expires_at,omitempty"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired,omitempty"`
}

type ImportResult struct {
	ProxyCreated   int           `json:"proxy_created"`
	ProxyReused    int           `json:"proxy_reused"`
	ProxyFailed    int           `json:"proxy_failed"`
	AccountCreated int           `json:"account_created"`
	AccountFailed  int           `json:"account_failed"`
	Errors         []ImportError `json:"errors,omitempty"`
}

type ImportError struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	ProxyKey string `json:"proxy_key,omitempty"`
	Message  string `json:"message"`
}

type PreparedImport struct {
	Payload  DataPayload                 `json:"payload"`
	Sources  []config.SubscriptionSource `json:"sources"`
	Accounts []config.Account            `json:"accounts"`
	Proxies  []config.ProxyConfig        `json:"proxies"`
	Result   ImportResult                `json:"result"`
}

func ValidatePayloadHeader(payload DataPayload) error {
	if payload.Type != "" && payload.Type != DataType && payload.Type != LegacyDataType {
		return fmt.Errorf("unsupported data type: %s", payload.Type)
	}
	if payload.Version != 0 && payload.Version != DataVersion {
		return fmt.Errorf("unsupported data version: %d", payload.Version)
	}
	if payload.Proxies == nil {
		return errors.New("proxies is required")
	}
	if payload.Accounts == nil {
		return errors.New("accounts is required")
	}
	return nil
}

func ValidateDataProxy(item DataProxy) error {
	if strings.TrimSpace(item.Protocol) == "" {
		return errors.New("proxy protocol is required")
	}
	if strings.TrimSpace(item.Host) == "" {
		return errors.New("proxy host is required")
	}
	if item.Port <= 0 || item.Port > 65535 {
		return errors.New("proxy port is invalid")
	}
	switch item.Protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("proxy protocol is invalid: %s", item.Protocol)
	}
	if item.Status != "" {
		normalizedStatus := NormalizeProxyStatus(item.Status)
		if normalizedStatus != StatusActive && normalizedStatus != "inactive" {
			return fmt.Errorf("proxy status is invalid: %s", item.Status)
		}
	}
	return nil
}

func ValidateDataAccount(item DataAccount) error {
	if strings.TrimSpace(item.Name) == "" {
		return errors.New("account name is required")
	}
	if strings.TrimSpace(item.Platform) == "" {
		return errors.New("account platform is required")
	}
	if strings.TrimSpace(item.Type) == "" {
		return errors.New("account type is required")
	}
	if len(item.Credentials) == 0 {
		return errors.New("account credentials is required")
	}
	switch item.Type {
	case AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey, AccountTypeUpstream:
	default:
		return fmt.Errorf("account type is invalid: %s", item.Type)
	}
	if item.RateMultiplier != nil && *item.RateMultiplier < 0 {
		return errors.New("rate_multiplier must be >= 0")
	}
	if item.Concurrency < 0 {
		return errors.New("concurrency must be >= 0")
	}
	if item.Priority < 0 {
		return errors.New("priority must be >= 0")
	}
	return nil
}

func PreparePayload(payload DataPayload, existing config.Config, sourceLabel string, tier string, tags []string, redacted bool) (PreparedImport, error) {
	if err := ValidatePayloadHeader(payload); err != nil {
		return PreparedImport{}, err
	}
	prepared := PreparedImport{Payload: payload}
	proxyKeyToID := map[string]string{}
	for _, proxy := range existing.Proxies {
		proxyKeyToID[proxy.ID] = proxy.ID
	}
	for i := range payload.Proxies {
		item := payload.Proxies[i]
		key := item.ProxyKey
		if key == "" {
			key = BuildProxyKey(item.Protocol, item.Host, item.Port, item.Username, item.Password)
		}
		if err := ValidateDataProxy(item); err != nil {
			prepared.Result.ProxyFailed++
			prepared.Result.Errors = append(prepared.Result.Errors, ImportError{Kind: "proxy", Name: item.Name, ProxyKey: key, Message: err.Error()})
			continue
		}
		proxyID := stableID("proxy", key)
		if _, ok := proxyKeyToID[key]; ok {
			prepared.Result.ProxyReused++
		} else {
			prepared.Result.ProxyCreated++
		}
		proxyKeyToID[key] = proxyID
		prepared.Proxies = append(prepared.Proxies, config.ProxyConfig{ID: proxyID, URL: DataProxyURL(item)})
	}
	sourceID := stableID("sub", sourceLabel+payload.ExportedAt+fmt.Sprint(len(payload.Accounts)))
	if sourceLabel == "" {
		sourceLabel = "sub2api Account Management import"
	}
	if tier == "" {
		tier = "simple"
	}
	prepared.Sources = append(prepared.Sources, config.SubscriptionSource{ID: sourceID, Kind: "json_bundle", Label: sourceLabel, Tier: tier, Tags: append([]string(nil), tags...), Enabled: true})
	for i := range payload.Accounts {
		item := payload.Accounts[i]
		if err := ValidateDataAccount(item); err != nil {
			prepared.Result.AccountFailed++
			prepared.Result.Errors = append(prepared.Result.Errors, ImportError{Kind: "account", Name: item.Name, Message: err.Error()})
			continue
		}
		var proxyRef string
		if item.ProxyKey != nil && *item.ProxyKey != "" {
			if id, ok := proxyKeyToID[*item.ProxyKey]; ok {
				proxyRef = id
			} else {
				prepared.Result.AccountFailed++
				prepared.Result.Errors = append(prepared.Result.Errors, ImportError{Kind: "account", Name: item.Name, ProxyKey: *item.ProxyKey, Message: "proxy_key not found"})
				continue
			}
		}
		credential := CredentialEnvelope(item.Credentials, redacted)
		account := config.Account{
			ID:         stableID("acct", item.Platform+"|"+item.Type+"|"+item.Name+"|"+credential),
			SourceID:   sourceID,
			Type:       LocalAccountType(item.Type),
			Label:      item.Name,
			BaseURL:    baseURLFromCredentials(item.Credentials),
			Tier:       tier,
			Tags:       append([]string(nil), tags...),
			Credential: credential,
			Metadata:   map[string]any{"platform": item.Platform, "account_category": item.Type},
			ProxyRef:   proxyRef,
			Enabled:    true,
		}
		prepared.Accounts = append(prepared.Accounts, account)
		prepared.Result.AccountCreated++
	}
	return prepared, nil
}

func BuildProxyKey(protocol, host string, port int, username string, password string) string {
	return fmt.Sprintf("%s|%s|%d|%s|%s", strings.TrimSpace(protocol), strings.TrimSpace(host), port, strings.TrimSpace(username), strings.TrimSpace(password))
}

func NormalizeProxyStatus(status string) string {
	s := strings.TrimSpace(strings.ToLower(status))
	if s == "enabled" || s == "active" {
		return StatusActive
	}
	if s == "disabled" || s == "inactive" {
		return "inactive"
	}
	return s
}

func DataProxyURL(item DataProxy) string {
	scheme := strings.TrimSpace(strings.ToLower(item.Protocol))
	if scheme == "socks5" {
		scheme = "socks5h"
	}
	host := net.JoinHostPort(strings.TrimSpace(item.Host), fmt.Sprintf("%d", item.Port))
	parsed := url.URL{Scheme: scheme, Host: host}
	if item.Username != "" || item.Password != "" {
		parsed.User = url.UserPassword(item.Username, item.Password)
	}
	return parsed.String()
}

func LocalAccountType(upstreamType string) string {
	switch upstreamType {
	case AccountTypeOAuth:
		return "oauth"
	case AccountTypeAPIKey:
		return "openai_api_key"
	default:
		return "openai_compatible"
	}
}

func CredentialEnvelope(credentials map[string]any, redacted bool) string {
	parts := make([]string, 0, len(credentials))
	keys := make([]string, 0, len(credentials))
	for key := range credentials {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := fmt.Sprint(credentials[key])
		if redacted {
			value = MaskSecret(value)
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ";")
}

func MaskPrepared(in PreparedImport) PreparedImport {
	out := in
	for i := range out.Accounts {
		out.Accounts[i].Credential = MaskSecret(out.Accounts[i].Credential)
	}
	for i := range out.Payload.Accounts {
		out.Payload.Accounts[i].Credentials = MaskCredentials(out.Payload.Accounts[i].Credentials)
	}
	return out
}

func MaskCredentials(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = MaskSecret(fmt.Sprint(value))
	}
	return out
}

func MaskSecret(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 8 {
		return "redacted"
	}
	return trimmed[:4] + "..." + trimmed[len(trimmed)-4:]
}

func PayloadFromLineTokens(content string, platform string, accountType string) DataPayload {
	now := time.Now().UTC().Format(time.RFC3339)
	payload := DataPayload{Type: DataType, Version: DataVersion, ExportedAt: now, Proxies: []DataProxy{}, Accounts: []DataAccount{}}
	for _, line := range strings.Split(content, "\n") {
		token := strings.TrimSpace(line)
		if token == "" || strings.HasPrefix(token, "#") {
			continue
		}
		payload.Accounts = append(payload.Accounts, DataAccount{
			Name:        "Imported " + stableID("", token)[:8],
			Platform:    platform,
			Type:        accountType,
			Credentials: credentialsForToken(platform, accountType, token),
		})
	}
	return payload
}

func credentialsForToken(platform string, accountType string, token string) map[string]any {
	switch accountType {
	case AccountTypeOAuth, AccountTypeSetupToken:
		return map[string]any{"refresh_token": token}
	case AccountTypeUpstream:
		return map[string]any{"api_key": token}
	default:
		if platform == PlatformAnthropic {
			return map[string]any{"api_key": token}
		}
		return map[string]any{"api_key": token}
	}
}

func baseURLFromCredentials(credentials map[string]any) string {
	for _, key := range []string{"base_url", "baseURL", "endpoint"} {
		if value := strings.TrimSpace(fmt.Sprint(credentials[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func stableID(prefix string, value string) string {
	sum := sha256.Sum256([]byte(value))
	id := hex.EncodeToString(sum[:])[:16]
	if prefix == "" {
		return id
	}
	return prefix + "_" + id
}
