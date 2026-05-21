package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const DefaultPath = "simple_sub2api.config.json"

var (
	ErrConfigVersionConflict = errors.New("config_version conflict")
	idPattern                = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{1,63}$`)
)

type Config struct {
	ConfigVersion       int                  `json:"config_version"`
	Server              ServerConfig         `json:"server"`
	GatewayAuth         GatewayAuthConfig    `json:"gateway_auth"`
	Dashboard           DashboardConfig      `json:"dashboard"`
	OAuthSources        []OAuthSource        `json:"oauth_sources"`
	SubscriptionSources []SubscriptionSource `json:"subscription_sources"`
	Accounts            []Account            `json:"accounts"`
	Proxies             []ProxyConfig        `json:"proxies"`
	Quota               QuotaConfig          `json:"quota"`
	Routing             RoutingConfig        `json:"routing"`
	Probe               ProbeConfig          `json:"probe"`
	Metrics             MetricsConfig        `json:"metrics"`
	UpstreamCompat      UpstreamCompatConfig `json:"upstreamcompat"`
}

type ServerConfig struct {
	Bind               string   `json:"bind"`
	AllowLAN           bool     `json:"allow_lan"`
	CORSAllowedOrigins []string `json:"cors_allowed_origins"`
}

type GatewayAuthConfig struct {
	GatewayKey string `json:"gateway_key"`
}

type DashboardConfig struct {
	AdminPassword     string `json:"admin_password,omitempty"`
	SessionTTLSeconds int    `json:"session_ttl_seconds"`
}

type OAuthSource struct {
	ID           string            `json:"id"`
	Platform     string            `json:"platform"`
	Type         string            `json:"type"`
	Label        string            `json:"label"`
	Tier         string            `json:"tier"`
	Tags         []string          `json:"tags"`
	Enabled      bool              `json:"enabled"`
	Credentials  map[string]string `json:"credentials,omitempty"`
	RefreshState RefreshState      `json:"refresh_state"`
	QuotaPolicy  string            `json:"quota_policy,omitempty"`
}

type RefreshState struct {
	Status        string `json:"status"`
	LastRefreshAt string `json:"last_refresh_at,omitempty"`
	NextRefreshAt string `json:"next_refresh_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

type SubscriptionSource struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	URL          string   `json:"url,omitempty"`
	InlineBundle string   `json:"inline_bundle,omitempty"`
	Label        string   `json:"label"`
	Tier         string   `json:"tier"`
	Tags         []string `json:"tags"`
	Enabled      bool     `json:"enabled"`
	LastSync     string   `json:"last_sync,omitempty"`
}

type Account struct {
	ID          string   `json:"id"`
	SourceID    string   `json:"source_id,omitempty"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	BaseURL     string   `json:"base_url,omitempty"`
	Model       string   `json:"model,omitempty"`
	Tier        string   `json:"tier"`
	Tags        []string `json:"tags"`
	Credential  string   `json:"credential,omitempty"`
	ProxyRef    string   `json:"proxy_ref,omitempty"`
	QuotaPolicy string   `json:"quota_policy,omitempty"`
	Enabled     bool     `json:"enabled"`
}

type ProxyConfig struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type QuotaConfig struct {
	WarnThreshold   float64       `json:"warn_threshold"`
	SwitchThreshold float64       `json:"switch_threshold"`
	Policies        []QuotaPolicy `json:"policies"`
}

type QuotaPolicy struct {
	ID                string `json:"id"`
	DailyLimitTokens  int64  `json:"daily_limit_tokens,omitempty"`
	WeeklyLimitTokens int64  `json:"weekly_limit_tokens,omitempty"`
	Source            string `json:"source"`
}

type RoutingConfig struct {
	DefaultTaskType string        `json:"default_task_type"`
	PreferTiers     []string      `json:"prefer_tiers"`
	FallbackTiers   []string      `json:"fallback_tiers"`
	Rules           []RoutingRule `json:"rules"`
}

type RoutingRule struct {
	TaskType      string   `json:"task_type"`
	ModelPatterns []string `json:"model_patterns"`
	Tags          []string `json:"tags"`
	PreferTiers   []string `json:"prefer_tiers"`
	FallbackTiers []string `json:"fallback_tiers"`
}

type ProbeConfig struct {
	TimeoutSeconds int    `json:"timeout_seconds"`
	Model          string `json:"model"`
	SavePolicy     string `json:"save_policy"`
}

type MetricsConfig struct {
	RecentErrorsLimit int `json:"recent_errors_limit"`
}

type UpstreamCompatConfig struct {
	Enabled      bool   `json:"enabled"`
	ManifestPath string `json:"manifest_path"`
}

type RuntimeOverrides struct {
	BindSet               bool
	Bind                  string
	AllowLANSet           bool
	AllowLAN              bool
	AdminPasswordSet      bool
	AdminPassword         string
	CORSAllowedOriginsSet bool
	CORSAllowedOrigins    []string
}

func (o RuntimeOverrides) HasAny() bool {
	return o.BindSet || o.AllowLANSet || o.AdminPasswordSet || o.CORSAllowedOriginsSet
}

type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
}

func DefaultConfig() Config {
	return Config{
		ConfigVersion: 1,
		Server: ServerConfig{
			Bind:               "127.0.0.1:8080",
			AllowLAN:           false,
			CORSAllowedOrigins: nil,
		},
		Dashboard: DashboardConfig{SessionTTLSeconds: 8 * 60 * 60},
		Quota: QuotaConfig{
			WarnThreshold:   0.80,
			SwitchThreshold: 0.95,
		},
		Routing: RoutingConfig{
			DefaultTaskType: "default",
			PreferTiers:     []string{"simple", "advanced"},
			FallbackTiers:   []string{"advanced", "simple"},
			Rules: []RoutingRule{
				{TaskType: "document", PreferTiers: []string{"simple"}, FallbackTiers: []string{"advanced"}},
				{TaskType: "code", PreferTiers: []string{"advanced"}, FallbackTiers: []string{"simple"}},
			},
		},
		Probe:          ProbeConfig{TimeoutSeconds: 15, Model: "gpt-4o-mini", SavePolicy: "save_disabled_on_error"},
		Metrics:        MetricsConfig{RecentErrorsLimit: 100},
		UpstreamCompat: UpstreamCompatConfig{Enabled: true, ManifestPath: "internal/upstreamcompat/SYNC_MANIFEST.md"},
	}
}

func Open(path string) (*Store, bool, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultPath
	}
	store := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, false, err
		}
		store.cfg = DefaultConfig()
		if err := EnsureDefaultsAndSecrets(&store.cfg); err != nil {
			return nil, false, err
		}
		if err := Validate(store.cfg); err != nil {
			return nil, false, err
		}
		if err := store.saveLocked(); err != nil {
			return nil, false, err
		}
		return store, true, nil
	}

	cfg := DefaultConfig()
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := DecodeStrict(strings.NewReader(string(data)), &cfg); err != nil {
			return nil, false, err
		}
	}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		return nil, false, err
	}
	if err := Validate(cfg); err != nil {
		return nil, false, err
	}
	store.cfg = cfg
	if err := store.saveLocked(); err != nil {
		return nil, false, err
	}
	return store, false, nil
}

func NewMemoryStore(cfg Config) (*Store, error) {
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		return nil, err
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return &Store{cfg: cfg}, nil
}

func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.cfg
	cfg.Server.CORSAllowedOrigins = append([]string(nil), s.cfg.Server.CORSAllowedOrigins...)
	cfg.OAuthSources = cloneOAuthSources(s.cfg.OAuthSources)
	cfg.SubscriptionSources = cloneSubscriptionSources(s.cfg.SubscriptionSources)
	cfg.Accounts = cloneAccounts(s.cfg.Accounts)
	cfg.Proxies = append([]ProxyConfig(nil), s.cfg.Proxies...)
	cfg.Quota.Policies = append([]QuotaPolicy(nil), s.cfg.Quota.Policies...)
	cfg.Routing.PreferTiers = append([]string(nil), s.cfg.Routing.PreferTiers...)
	cfg.Routing.FallbackTiers = append([]string(nil), s.cfg.Routing.FallbackTiers...)
	cfg.Routing.Rules = cloneRoutingRules(s.cfg.Routing.Rules)
	return cfg
}

func (s *Store) GatewayKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.GatewayAuth.GatewayKey
}

func (s *Store) ApplyOverrides(overrides RuntimeOverrides) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := s.cfg
	if overrides.BindSet {
		candidate.Server.Bind = overrides.Bind
	}
	if overrides.AllowLANSet {
		candidate.Server.AllowLAN = overrides.AllowLAN
	}
	if overrides.AdminPasswordSet {
		candidate.Dashboard.AdminPassword = overrides.AdminPassword
	}
	if overrides.CORSAllowedOriginsSet {
		candidate.Server.CORSAllowedOrigins = append([]string(nil), overrides.CORSAllowedOrigins...)
	}
	if err := EnsureDefaultsAndSecrets(&candidate); err != nil {
		return err
	}
	if err := Validate(candidate); err != nil {
		return err
	}
	candidate.ConfigVersion = s.cfg.ConfigVersion + 1
	if err := s.saveConfigLocked(candidate); err != nil {
		return err
	}
	s.cfg = candidate
	return nil
}

func (s *Store) RotateGatewayKey() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := GenerateGatewayKey()
	if err != nil {
		return "", err
	}
	candidate := s.cfg
	candidate.GatewayAuth.GatewayKey = key
	candidate.ConfigVersion = s.cfg.ConfigVersion + 1
	if err := Validate(candidate); err != nil {
		return "", err
	}
	if err := s.saveConfigLocked(candidate); err != nil {
		return "", err
	}
	s.cfg = candidate
	return key, nil
}

func (s *Store) Update(expectedVersion int, mutate func(*Config) error) (Config, error) {
	return s.UpdateValidated(expectedVersion, mutate, nil)
}

func (s *Store) UpdateValidated(expectedVersion int, mutate func(*Config) error, validateCandidate func(Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedVersion != s.cfg.ConfigVersion {
		return Config{}, ErrConfigVersionConflict
	}
	candidate := s.cfg
	if err := mutate(&candidate); err != nil {
		return Config{}, err
	}
	if err := EnsureDefaultsAndSecrets(&candidate); err != nil {
		return Config{}, err
	}
	if err := Validate(candidate); err != nil {
		return Config{}, err
	}
	if validateCandidate != nil {
		if err := validateCandidate(cloneConfig(candidate)); err != nil {
			return Config{}, err
		}
	}
	candidate.ConfigVersion = s.cfg.ConfigVersion + 1
	if err := s.saveConfigLocked(candidate); err != nil {
		return Config{}, err
	}
	s.cfg = candidate
	return cloneConfig(candidate), nil
}

func EnsureDefaultsAndSecrets(cfg *Config) error {
	if cfg.ConfigVersion == 0 {
		cfg.ConfigVersion = 1
	}
	if strings.TrimSpace(cfg.Server.Bind) == "" {
		cfg.Server.Bind = "127.0.0.1:8080"
	}
	if cfg.Dashboard.SessionTTLSeconds <= 0 {
		cfg.Dashboard.SessionTTLSeconds = 8 * 60 * 60
	}
	if strings.TrimSpace(cfg.GatewayAuth.GatewayKey) == "" {
		key, err := GenerateGatewayKey()
		if err != nil {
			return err
		}
		cfg.GatewayAuth.GatewayKey = key
	}
	if cfg.Quota.WarnThreshold == 0 {
		cfg.Quota.WarnThreshold = 0.80
	}
	if cfg.Quota.SwitchThreshold == 0 {
		cfg.Quota.SwitchThreshold = 0.95
	}
	if strings.TrimSpace(cfg.Routing.DefaultTaskType) == "" {
		cfg.Routing.DefaultTaskType = "default"
	}
	if cfg.Probe.TimeoutSeconds <= 0 {
		cfg.Probe.TimeoutSeconds = 15
	}
	if strings.TrimSpace(cfg.Probe.Model) == "" {
		cfg.Probe.Model = "gpt-4o-mini"
	}
	if strings.TrimSpace(cfg.Probe.SavePolicy) == "" {
		cfg.Probe.SavePolicy = "save_disabled_on_error"
	}
	if cfg.Metrics.RecentErrorsLimit <= 0 {
		cfg.Metrics.RecentErrorsLimit = 100
	}
	if strings.TrimSpace(cfg.UpstreamCompat.ManifestPath) == "" {
		cfg.UpstreamCompat.ManifestPath = "internal/upstreamcompat/SYNC_MANIFEST.md"
	}
	for i := range cfg.Proxies {
		cfg.Proxies[i].URL = NormalizeProxyURL(cfg.Proxies[i].URL)
	}
	return nil
}

func Validate(cfg Config) error {
	if cfg.ConfigVersion <= 0 {
		return errors.New("config_version must be positive")
	}
	host, portText, err := net.SplitHostPort(cfg.Server.Bind)
	if err != nil {
		return fmt.Errorf("server.bind must be host:port: %w", err)
	}
	if strings.TrimSpace(host) == "" {
		return errors.New("server.bind host must be explicit; use 127.0.0.1 for local mode")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("server.bind port must be in range 1..65535")
	}
	if IsLANBind(cfg.Server.Bind) {
		if !cfg.Server.AllowLAN {
			return errors.New("non-loopback bind requires server.allow_lan=true")
		}
		if strings.TrimSpace(cfg.Dashboard.AdminPassword) == "" {
			return errors.New("LAN bind requires dashboard.admin_password")
		}
	}
	if !strings.HasPrefix(cfg.GatewayAuth.GatewayKey, "s2a_") || len(cfg.GatewayAuth.GatewayKey) < 24 {
		return errors.New("gateway key must use project-owned s2a_ style")
	}
	if cfg.Dashboard.AdminPassword != "" && cfg.Dashboard.AdminPassword == cfg.GatewayAuth.GatewayKey {
		return errors.New("dashboard admin password must not equal gateway key")
	}
	for _, origin := range cfg.Server.CORSAllowedOrigins {
		if origin == "*" {
			return errors.New("wildcard CORS origin is not allowed")
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("invalid CORS origin %q", origin)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("invalid CORS origin scheme %q", parsed.Scheme)
		}
	}
	if err := validateOAuthSources(cfg.OAuthSources); err != nil {
		return err
	}
	if err := validateSubscriptionSources(cfg.SubscriptionSources); err != nil {
		return err
	}
	if err := validateProxies(cfg.Proxies); err != nil {
		return err
	}
	if err := validateQuota(cfg.Quota); err != nil {
		return err
	}
	if err := validateRouting(cfg.Routing); err != nil {
		return err
	}
	if err := validateAccounts(cfg.Accounts, cfg); err != nil {
		return err
	}
	if cfg.Probe.TimeoutSeconds < 1 || cfg.Probe.TimeoutSeconds > 300 {
		return errors.New("probe.timeout_seconds must be in range 1..300")
	}
	if cfg.Probe.SavePolicy != "save_disabled_on_error" && cfg.Probe.SavePolicy != "healthy_only" {
		return errors.New("probe.save_policy must be save_disabled_on_error or healthy_only")
	}
	if cfg.Metrics.RecentErrorsLimit < 1 || cfg.Metrics.RecentErrorsLimit > 1000 {
		return errors.New("metrics.recent_errors_limit must be in range 1..1000")
	}
	return nil
}

func DecodeStrict(r io.Reader, cfg *Config) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(cfg); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("config must contain a single JSON object")
	}
	return nil
}

func Redacted(cfg Config) Config {
	cfg.GatewayAuth.GatewayKey = maskSecret(cfg.GatewayAuth.GatewayKey)
	if cfg.Dashboard.AdminPassword != "" {
		cfg.Dashboard.AdminPassword = "configured"
	}
	for i := range cfg.OAuthSources {
		cfg.OAuthSources[i].Credentials = redactMap(cfg.OAuthSources[i].Credentials)
	}
	for i := range cfg.SubscriptionSources {
		cfg.SubscriptionSources[i].InlineBundle = maskSecret(cfg.SubscriptionSources[i].InlineBundle)
	}
	for i := range cfg.Accounts {
		cfg.Accounts[i].Credential = maskSecret(cfg.Accounts[i].Credential)
	}
	return cfg
}

func NormalizeProxyURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(trimmed), "socks5://") {
		return "socks5h://" + trimmed[len("socks5://"):]
	}
	return trimmed
}

func validateOAuthSources(sources []OAuthSource) error {
	seen := map[string]bool{}
	for _, source := range sources {
		if err := validateID("oauth_sources.id", source.ID); err != nil {
			return err
		}
		if seen[source.ID] {
			return fmt.Errorf("duplicate oauth source id %q", source.ID)
		}
		seen[source.ID] = true
		if strings.TrimSpace(source.Platform) == "" {
			return fmt.Errorf("oauth source %q platform is required", source.ID)
		}
		if !oneOf(source.Type, "oauth", "token_bundle", "manual") {
			return fmt.Errorf("oauth source %q type must be oauth, token_bundle, or manual", source.ID)
		}
		if strings.TrimSpace(source.Label) == "" {
			return fmt.Errorf("oauth source %q label is required", source.ID)
		}
		if strings.TrimSpace(source.Tier) == "" {
			return fmt.Errorf("oauth source %q tier is required", source.ID)
		}
		if source.RefreshState.Status != "" && !oneOf(source.RefreshState.Status, "unknown", "ok", "needs_auth", "refresh_error", "disabled") {
			return fmt.Errorf("oauth source %q refresh_state.status is invalid", source.ID)
		}
	}
	return nil
}

func validateSubscriptionSources(sources []SubscriptionSource) error {
	seen := map[string]bool{}
	for _, source := range sources {
		if err := validateID("subscription_sources.id", source.ID); err != nil {
			return err
		}
		if seen[source.ID] {
			return fmt.Errorf("duplicate subscription source id %q", source.ID)
		}
		seen[source.ID] = true
		if !oneOf(source.Kind, "line_tokens", "json_bundle", "url", "inline_bundle") {
			return fmt.Errorf("subscription source %q kind must be line_tokens, json_bundle, url, or inline_bundle", source.ID)
		}
		if strings.TrimSpace(source.Label) == "" {
			return fmt.Errorf("subscription source %q label is required", source.ID)
		}
		if strings.TrimSpace(source.Tier) == "" {
			return fmt.Errorf("subscription source %q tier is required", source.ID)
		}
		if source.Kind == "url" {
			if err := validateHTTPURL("subscription source URL", source.URL); err != nil {
				return fmt.Errorf("subscription source %q: %w", source.ID, err)
			}
		}
		if source.Kind != "url" && strings.TrimSpace(source.URL) != "" {
			return fmt.Errorf("subscription source %q URL is only valid for url kind", source.ID)
		}
		if source.Kind == "inline_bundle" && strings.TrimSpace(source.InlineBundle) == "" {
			return fmt.Errorf("subscription source %q inline_bundle is required", source.ID)
		}
	}
	return nil
}

func validateProxies(proxies []ProxyConfig) error {
	seen := map[string]bool{}
	for _, proxy := range proxies {
		if err := validateID("proxies.id", proxy.ID); err != nil {
			return err
		}
		if seen[proxy.ID] {
			return fmt.Errorf("duplicate proxy id %q", proxy.ID)
		}
		seen[proxy.ID] = true
		parsed, err := url.Parse(proxy.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("proxy %q URL must be absolute", proxy.ID)
		}
		if !oneOf(parsed.Scheme, "http", "https", "socks5h") {
			return fmt.Errorf("proxy %q scheme must be http, https, or socks5h", proxy.ID)
		}
	}
	return nil
}

func validateQuota(quota QuotaConfig) error {
	if quota.WarnThreshold <= 0 || quota.WarnThreshold >= 1 {
		return errors.New("quota.warn_threshold must be greater than 0 and less than 1")
	}
	if quota.SwitchThreshold <= 0 || quota.SwitchThreshold > 1 {
		return errors.New("quota.switch_threshold must be greater than 0 and at most 1")
	}
	if quota.WarnThreshold >= quota.SwitchThreshold {
		return errors.New("quota.warn_threshold must be lower than quota.switch_threshold")
	}
	seen := map[string]bool{}
	for _, policy := range quota.Policies {
		if err := validateID("quota.policies.id", policy.ID); err != nil {
			return err
		}
		if seen[policy.ID] {
			return fmt.Errorf("duplicate quota policy id %q", policy.ID)
		}
		seen[policy.ID] = true
		if policy.DailyLimitTokens < 0 || policy.WeeklyLimitTokens < 0 {
			return fmt.Errorf("quota policy %q token limits must not be negative", policy.ID)
		}
		if strings.TrimSpace(policy.Source) == "" {
			return fmt.Errorf("quota policy %q source is required", policy.ID)
		}
	}
	return nil
}

func validateRouting(routing RoutingConfig) error {
	if !oneOf(routing.DefaultTaskType, "default", "document", "code") {
		return errors.New("routing.default_task_type must be default, document, or code")
	}
	for _, rule := range routing.Rules {
		if !oneOf(rule.TaskType, "default", "document", "code") {
			return fmt.Errorf("routing rule task_type %q is invalid", rule.TaskType)
		}
	}
	return nil
}

func validateAccounts(accounts []Account, cfg Config) error {
	seen := map[string]bool{}
	proxyIDs := map[string]bool{}
	for _, proxy := range cfg.Proxies {
		proxyIDs[proxy.ID] = true
	}
	quotaIDs := map[string]bool{}
	for _, policy := range cfg.Quota.Policies {
		quotaIDs[policy.ID] = true
	}
	sourceIDs := map[string]bool{}
	for _, source := range cfg.OAuthSources {
		sourceIDs[source.ID] = true
	}
	for _, source := range cfg.SubscriptionSources {
		sourceIDs[source.ID] = true
	}
	for _, account := range accounts {
		if err := validateID("accounts.id", account.ID); err != nil {
			return err
		}
		if seen[account.ID] {
			return fmt.Errorf("duplicate account id %q", account.ID)
		}
		seen[account.ID] = true
		if !oneOf(account.Type, "openai_api_key", "oauth", "openai_compatible") {
			return fmt.Errorf("account %q type must be openai_api_key, oauth, or openai_compatible", account.ID)
		}
		if strings.TrimSpace(account.Label) == "" {
			return fmt.Errorf("account %q label is required", account.ID)
		}
		if strings.TrimSpace(account.Tier) == "" {
			return fmt.Errorf("account %q tier is required", account.ID)
		}
		if strings.TrimSpace(account.Credential) == "" {
			return fmt.Errorf("account %q credential is required", account.ID)
		}
		if account.SourceID != "" && !sourceIDs[account.SourceID] {
			return fmt.Errorf("account %q references unknown source %q", account.ID, account.SourceID)
		}
		if account.ProxyRef != "" && !proxyIDs[account.ProxyRef] {
			return fmt.Errorf("account %q references unknown proxy %q", account.ID, account.ProxyRef)
		}
		if account.QuotaPolicy != "" && !quotaIDs[account.QuotaPolicy] {
			return fmt.Errorf("account %q references unknown quota policy %q", account.ID, account.QuotaPolicy)
		}
		if account.BaseURL != "" {
			if err := validateHTTPURL("account base_url", account.BaseURL); err != nil {
				return fmt.Errorf("account %q: %w", account.ID, err)
			}
		}
	}
	return nil
}

func validateID(field string, value string) error {
	if !idPattern.MatchString(value) {
		return fmt.Errorf("%s must match %s", field, idPattern.String())
	}
	return nil
}

func validateHTTPURL(field string, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", field)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s scheme must be http or https", field)
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func cloneConfig(cfg Config) Config {
	cfg.Server.CORSAllowedOrigins = append([]string(nil), cfg.Server.CORSAllowedOrigins...)
	cfg.OAuthSources = cloneOAuthSources(cfg.OAuthSources)
	cfg.SubscriptionSources = cloneSubscriptionSources(cfg.SubscriptionSources)
	cfg.Accounts = cloneAccounts(cfg.Accounts)
	cfg.Proxies = append([]ProxyConfig(nil), cfg.Proxies...)
	cfg.Quota.Policies = append([]QuotaPolicy(nil), cfg.Quota.Policies...)
	cfg.Routing.PreferTiers = append([]string(nil), cfg.Routing.PreferTiers...)
	cfg.Routing.FallbackTiers = append([]string(nil), cfg.Routing.FallbackTiers...)
	cfg.Routing.Rules = cloneRoutingRules(cfg.Routing.Rules)
	return cfg
}

func cloneOAuthSources(sources []OAuthSource) []OAuthSource {
	out := make([]OAuthSource, len(sources))
	copy(out, sources)
	for i := range out {
		out[i].Tags = append([]string(nil), sources[i].Tags...)
		out[i].Credentials = cloneStringMap(sources[i].Credentials)
	}
	return out
}

func cloneSubscriptionSources(sources []SubscriptionSource) []SubscriptionSource {
	out := make([]SubscriptionSource, len(sources))
	copy(out, sources)
	for i := range out {
		out[i].Tags = append([]string(nil), sources[i].Tags...)
	}
	return out
}

func cloneAccounts(accounts []Account) []Account {
	out := make([]Account, len(accounts))
	copy(out, accounts)
	for i := range out {
		out[i].Tags = append([]string(nil), accounts[i].Tags...)
	}
	return out
}

func cloneRoutingRules(rules []RoutingRule) []RoutingRule {
	out := make([]RoutingRule, len(rules))
	copy(out, rules)
	for i := range out {
		out[i].ModelPatterns = append([]string(nil), rules[i].ModelPatterns...)
		out[i].Tags = append([]string(nil), rules[i].Tags...)
		out[i].PreferTiers = append([]string(nil), rules[i].PreferTiers...)
		out[i].FallbackTiers = append([]string(nil), rules[i].FallbackTiers...)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func redactMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = maskSecret(value)
	}
	return out
}

func maskSecret(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	if len(value) <= 8 {
		return "redacted"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func IsLANBind(bind string) bool {
	return !IsLoopbackBind(bind)
}

func IsLoopbackBind(bind string) bool {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func GenerateGatewayKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "s2a_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Store) saveLocked() error {
	return s.saveConfigLocked(s.cfg)
}

func (s *Store) saveConfigLocked(cfg Config) error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
