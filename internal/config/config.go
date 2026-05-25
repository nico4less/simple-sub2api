package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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
	tunnelTokenPattern       = regexp.MustCompile(`^[A-Za-z0-9._=-]+$`)
)

type Config struct {
	ConfigVersion       int                  `json:"config_version"`
	Server              ServerConfig         `json:"server"`
	GatewayAuth         GatewayAuthConfig    `json:"gateway_auth"`
	GatewayKeys         []GatewayKey         `json:"gateway_keys"`
	Groups              []Group              `json:"groups"`
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
	Tunnel              TunnelConfig         `json:"tunnel,omitempty"`
}

type ServerConfig struct {
	Bind               string   `json:"bind"`
	AllowLAN           bool     `json:"allow_lan"`
	CORSAllowedOrigins []string `json:"cors_allowed_origins"`
}

type GatewayAuthConfig struct {
	GatewayKey string `json:"gateway_key"`
}

type GatewayKey struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	KeyHash       string           `json:"key_hash"`
	KeyValue      string           `json:"key_value,omitempty"`
	Preview       string           `json:"preview"`
	Status        string           `json:"status"`
	RoutingPolicy KeyRoutingPolicy `json:"routing_policy"`
	CreatedAt     string           `json:"created_at"`
	UpdatedAt     string           `json:"updated_at"`
	LastUsedAt    string           `json:"last_used_at,omitempty"`
	Note          string           `json:"note,omitempty"`
}

type KeyRoutingPolicy struct {
	Mode       string   `json:"mode"`
	GroupIDs   []string `json:"group_ids,omitempty"`
	Tiers      []string `json:"tiers,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	AccountIDs []string `json:"account_ids,omitempty"`
}

type Group struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Platform       string              `json:"platform"`
	Description    string              `json:"description,omitempty"`
	Status         string              `json:"status"`
	Tags           []string            `json:"tags,omitempty"`
	AccountIDs     []string            `json:"account_ids,omitempty"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	RotationPolicy GroupRotationPolicy `json:"rotation_policy,omitempty"`
}

type GroupRotationPolicy struct {
	Strategy                 string  `json:"strategy"`
	StickySessionsEnabled    bool    `json:"sticky_sessions_enabled"`
	StickyHeader             string  `json:"sticky_header,omitempty"`
	RetryOnErrors            bool    `json:"retry_on_errors"`
	RotateErrorCodes         []int   `json:"rotate_error_codes,omitempty"`
	CooldownDurationSeconds  int     `json:"cooldown_duration_seconds,omitempty"`
	EnableQuotaProtection    bool    `json:"enable_quota_protection"`
	MinQuotaThresholdPercent float64 `json:"min_quota_threshold_percent,omitempty"`
}

type GatewayKeyMatch struct {
	Key GatewayKey
	OK  bool
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
	ID          string         `json:"id"`
	SourceID    string         `json:"source_id,omitempty"`
	Type        string         `json:"type"`
	Label       string         `json:"label"`
	BaseURL     string         `json:"base_url,omitempty"`
	Model       string         `json:"model,omitempty"`
	Tier        string         `json:"tier"`
	Tags        []string       `json:"tags"`
	Credential  string         `json:"credential,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	ProxyRef    string         `json:"proxy_ref,omitempty"`
	QuotaPolicy string         `json:"quota_policy,omitempty"`
	Enabled     bool           `json:"enabled"`
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

type TunnelConfig struct {
	Enabled       bool   `json:"enabled"`
	Mode          string `json:"mode"`
	BinaryPath    string `json:"binary_path,omitempty"`
	Token         string `json:"token,omitempty"`
	LogLimitLines int    `json:"log_limit_lines,omitempty"`
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
		Groups: []Group{
			defaultOpenAIGroup(),
		},
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
		Tunnel:         TunnelConfig{Mode: "quick", LogLimitLines: 100},
	}
}

func defaultOpenAIGroup() Group {
	return Group{ID: "openai", Name: "openai", Platform: "openai", Description: "Default OpenAI routing group", Status: "active", CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z"}
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
	cfg.GatewayKeys = cloneGatewayKeys(s.cfg.GatewayKeys)
	cfg.Groups = cloneGroups(s.cfg.Groups)
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

func (s *Store) Path() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.path
}

func (s *Store) MatchGatewayKey(value string) GatewayKeyMatch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return MatchGatewayKey(s.cfg, value)
}

func (s *Store) TouchGatewayKeyLastUsed(id string, usedAt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if err := s.reloadLocked(); err != nil {
		return err
	}
	candidate := cloneConfig(s.cfg)
	for i := range candidate.GatewayKeys {
		if candidate.GatewayKeys[i].ID != id {
			continue
		}
		candidate.GatewayKeys[i].LastUsedAt = usedAt
		if err := s.saveConfigLocked(candidate); err != nil {
			return err
		}
		s.cfg = candidate
		return nil
	}
	return nil
}

func (s *Store) DisableAccount(accountID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(accountID) == "" {
		return false, nil
	}
	if err := s.reloadLocked(); err != nil {
		return false, err
	}
	candidate := cloneConfig(s.cfg)
	for i := range candidate.Accounts {
		if candidate.Accounts[i].ID != accountID {
			continue
		}
		if !candidate.Accounts[i].Enabled {
			return false, nil
		}
		candidate.Accounts[i].Enabled = false
		candidate.ConfigVersion = s.cfg.ConfigVersion + 1
		if err := EnsureDefaultsAndSecrets(&candidate); err != nil {
			return false, err
		}
		if err := Validate(candidate); err != nil {
			return false, err
		}
		if err := s.saveConfigLocked(candidate); err != nil {
			return false, err
		}
		s.cfg = candidate
		return true, nil
	}
	return false, nil
}

type OpenAIAccessTokenUpdate struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    string
}

type AccountMetadataUpdate struct {
	Metadata map[string]any
}

func (s *Store) UpdateOpenAIAccessToken(accountID string, token OpenAIAccessTokenUpdate) (Account, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(token.AccessToken) == "" {
		return Account{}, false, nil
	}
	if err := s.reloadLocked(); err != nil {
		return Account{}, false, err
	}
	candidate := cloneConfig(s.cfg)
	for i := range candidate.Accounts {
		if candidate.Accounts[i].ID != accountID {
			continue
		}
		candidate.Accounts[i].Credential = mergeCredential(candidate.Accounts[i].Credential, map[string]string{
			"access_token": token.AccessToken,
			"expires_at":   token.ExpiresAt,
		})
		if strings.TrimSpace(token.RefreshToken) != "" {
			candidate.Accounts[i].Credential = mergeCredential(candidate.Accounts[i].Credential, map[string]string{"refresh_token": token.RefreshToken})
		}
		if strings.TrimSpace(token.IDToken) != "" {
			candidate.Accounts[i].Credential = mergeCredential(candidate.Accounts[i].Credential, map[string]string{"id_token": token.IDToken})
		}
		candidate.Accounts[i].Enabled = true
		candidate.ConfigVersion = s.cfg.ConfigVersion + 1
		if err := EnsureDefaultsAndSecrets(&candidate); err != nil {
			return Account{}, false, err
		}
		if err := Validate(candidate); err != nil {
			return Account{}, false, err
		}
		if err := s.saveConfigLocked(candidate); err != nil {
			return Account{}, false, err
		}
		s.cfg = candidate
		return cloneAccounts([]Account{candidate.Accounts[i]})[0], true, nil
	}
	return Account{}, false, nil
}

func (s *Store) UpdateAccountMetadata(accountID string, update AccountMetadataUpdate) (Account, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(accountID) == "" || len(update.Metadata) == 0 {
		return Account{}, false, nil
	}
	if err := s.reloadLocked(); err != nil {
		return Account{}, false, err
	}
	candidate := cloneConfig(s.cfg)
	for i := range candidate.Accounts {
		if candidate.Accounts[i].ID != accountID {
			continue
		}
		metadata := cloneAnyMap(candidate.Accounts[i].Metadata)
		if metadata == nil {
			metadata = map[string]any{}
		}
		for key, value := range update.Metadata {
			if strings.TrimSpace(key) == "" {
				continue
			}
			metadata[key] = value
		}
		candidate.Accounts[i].Metadata = metadata
		candidate.ConfigVersion = s.cfg.ConfigVersion + 1
		if err := EnsureDefaultsAndSecrets(&candidate); err != nil {
			return Account{}, false, err
		}
		if err := Validate(candidate); err != nil {
			return Account{}, false, err
		}
		if err := s.saveConfigLocked(candidate); err != nil {
			return Account{}, false, err
		}
		s.cfg = candidate
		return cloneAccounts([]Account{candidate.Accounts[i]})[0], true, nil
	}
	return Account{}, false, nil
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
	if len(candidate.GatewayKeys) > 0 {
		candidate.GatewayKeys[0].KeyHash = HashGatewayKey(key)
		candidate.GatewayKeys[0].Preview = KeyPreview(key)
	}
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
	if err := s.reloadLocked(); err != nil {
		return Config{}, err
	}
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
	now := "1970-01-01T00:00:00Z"
	if cfg.GatewayKeys == nil && strings.TrimSpace(cfg.GatewayAuth.GatewayKey) != "" {
		cfg.GatewayKeys = []GatewayKey{{
			ID:        "default",
			Name:      "Default Gateway Key",
			KeyHash:   HashGatewayKey(cfg.GatewayAuth.GatewayKey),
			Preview:   KeyPreview(cfg.GatewayAuth.GatewayKey),
			Status:    "enabled",
			CreatedAt: now,
			UpdatedAt: now,
			RoutingPolicy: KeyRoutingPolicy{
				Mode: "all_enabled",
			},
		}}
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
	if cfg.Groups == nil {
		cfg.Groups = DefaultConfig().Groups
	}
	if !hasGroupID(cfg.Groups, "openai") {
		cfg.Groups = append([]Group{defaultOpenAIGroup()}, cfg.Groups...)
	}
	for i := range cfg.Groups {
		ensureGroupRotationDefaults(&cfg.Groups[i].RotationPolicy)
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
	ensureTunnelDefaults(&cfg.Tunnel)
	if strings.TrimSpace(cfg.UpstreamCompat.ManifestPath) == "" {
		cfg.UpstreamCompat.ManifestPath = "internal/upstreamcompat/SYNC_MANIFEST.md"
	}
	for i := range cfg.Proxies {
		cfg.Proxies[i].URL = NormalizeProxyURL(cfg.Proxies[i].URL)
	}
	return nil
}

func hasGroupID(groups []Group, id string) bool {
	for _, group := range groups {
		if group.ID == id {
			return true
		}
	}
	return false
}

func ensureGroupRotationDefaults(policy *GroupRotationPolicy) {
	if strings.TrimSpace(policy.Strategy) == "" {
		policy.Strategy = "polling"
	}
	if strings.TrimSpace(policy.StickyHeader) == "" {
		policy.StickyHeader = "X-Session-ID"
	}
	if len(policy.RotateErrorCodes) == 0 {
		policy.RotateErrorCodes = []int{429, 401, 403, 404, 500}
	}
	if policy.CooldownDurationSeconds == 0 {
		policy.CooldownDurationSeconds = 60
	}
	if policy.MinQuotaThresholdPercent == 0 {
		policy.MinQuotaThresholdPercent = 0.10
	}
}

func ensureTunnelDefaults(tunnel *TunnelConfig) {
	tunnel.Mode = strings.TrimSpace(tunnel.Mode)
	if tunnel.Mode == "" {
		tunnel.Mode = "quick"
	}
	tunnel.BinaryPath = strings.TrimSpace(tunnel.BinaryPath)
	tunnel.Token = strings.TrimSpace(tunnel.Token)
	if tunnel.LogLimitLines == 0 {
		tunnel.LogLimitLines = 100
	}
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
	if err := validateGatewayKeys(cfg.GatewayKeys, cfg); err != nil {
		return err
	}
	if err := validateGroups(cfg.Groups, cfg); err != nil {
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
	if err := validateTunnel(cfg.Tunnel); err != nil {
		return err
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
	cfg.GatewayKeys = cloneGatewayKeys(cfg.GatewayKeys)
	for i := range cfg.GatewayKeys {
		cfg.GatewayKeys[i].KeyValue = ""
	}
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
	cfg.Tunnel.Token = maskSecret(cfg.Tunnel.Token)
	return cfg
}

func validateTunnel(tunnel TunnelConfig) error {
	if !oneOf(tunnel.Mode, "quick", "named") {
		return errors.New("tunnel.mode must be quick or named")
	}
	if tunnel.LogLimitLines < 10 || tunnel.LogLimitLines > 1000 {
		return errors.New("tunnel.log_limit_lines must be in range 10..1000")
	}
	if tunnel.Token != "" {
		if len(tunnel.Token) < 32 || len(tunnel.Token) > 4096 || !tunnelTokenPattern.MatchString(tunnel.Token) {
			return errors.New("tunnel.token must be a valid Cloudflare tunnel token")
		}
	}
	if tunnel.Enabled && tunnel.Mode == "named" && tunnel.Token == "" {
		return errors.New("tunnel.token is required when tunnel.enabled=true and tunnel.mode=named")
	}
	return nil
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

func validateGatewayKeys(keys []GatewayKey, cfg Config) error {
	seen := map[string]bool{}
	accountIDs := map[string]bool{}
	for _, account := range cfg.Accounts {
		accountIDs[account.ID] = true
	}
	groupIDs := map[string]bool{}
	for _, group := range cfg.Groups {
		groupIDs[group.ID] = true
	}
	for _, key := range keys {
		if err := validateID("gateway_keys.id", key.ID); err != nil {
			return err
		}
		if seen[key.ID] {
			return fmt.Errorf("duplicate gateway key id %q", key.ID)
		}
		seen[key.ID] = true
		if strings.TrimSpace(key.Name) == "" {
			return fmt.Errorf("gateway key %q name is required", key.ID)
		}
		if key.Status != "enabled" && key.Status != "disabled" {
			return fmt.Errorf("gateway key %q status must be enabled or disabled", key.ID)
		}
		if !strings.HasPrefix(key.KeyHash, "sha256:") || len(key.KeyHash) != len("sha256:")+64 {
			return fmt.Errorf("gateway key %q key_hash must use sha256 hex format", key.ID)
		}
		if strings.TrimSpace(key.KeyValue) != "" && key.KeyHash != HashGatewayKey(key.KeyValue) {
			return fmt.Errorf("gateway key %q key_value does not match key_hash", key.ID)
		}
		if strings.TrimSpace(key.Preview) == "" {
			return fmt.Errorf("gateway key %q preview is required", key.ID)
		}
		if strings.TrimSpace(key.CreatedAt) == "" || strings.TrimSpace(key.UpdatedAt) == "" {
			return fmt.Errorf("gateway key %q timestamps are required", key.ID)
		}
		policy := key.RoutingPolicy
		if policy.Mode == "" {
			policy.Mode = "all_enabled"
		}
		if !oneOf(policy.Mode, "all_enabled", "tier_preference", "tags", "account_ids", "groups") {
			return fmt.Errorf("gateway key %q routing_policy.mode is invalid", key.ID)
		}
		for _, groupID := range policy.GroupIDs {
			if !groupIDs[groupID] {
				return fmt.Errorf("gateway key %q references unknown group %q", key.ID, groupID)
			}
		}
		for _, accountID := range policy.AccountIDs {
			if !accountIDs[accountID] {
				return fmt.Errorf("gateway key %q references unknown account %q", key.ID, accountID)
			}
		}
	}
	return nil
}

func validateGroups(groups []Group, cfg Config) error {
	seen := map[string]bool{}
	accountsByID := map[string]Account{}
	for _, account := range cfg.Accounts {
		accountsByID[account.ID] = account
	}
	for _, group := range groups {
		if err := validateID("groups.id", group.ID); err != nil {
			return err
		}
		if seen[group.ID] {
			return fmt.Errorf("duplicate group id %q", group.ID)
		}
		seen[group.ID] = true
		if strings.TrimSpace(group.Name) == "" {
			return fmt.Errorf("group %q name is required", group.ID)
		}
		if !oneOf(group.Platform, "openai", "anthropic", "gemini", "antigravity") {
			return fmt.Errorf("group %q platform is invalid", group.ID)
		}
		if group.Status != "active" && group.Status != "disabled" {
			return fmt.Errorf("group %q status must be active or disabled", group.ID)
		}
		if strings.TrimSpace(group.CreatedAt) == "" || strings.TrimSpace(group.UpdatedAt) == "" {
			return fmt.Errorf("group %q timestamps are required", group.ID)
		}
		if err := validateGroupRotationPolicy(group.ID, group.RotationPolicy); err != nil {
			return err
		}
		for _, accountID := range group.AccountIDs {
			account, ok := accountsByID[accountID]
			if !ok {
				return fmt.Errorf("group %q references unknown account %q", group.ID, accountID)
			}
			accountPlatform := AccountPlatform(account)
			if accountPlatform == "" {
				return fmt.Errorf("group %q account %q platform is required", group.ID, accountID)
			}
			if accountPlatform != group.Platform {
				return fmt.Errorf("group %q platform %q cannot include %q account %q", group.ID, group.Platform, accountPlatform, accountID)
			}
		}
	}
	return nil
}

func validateGroupRotationPolicy(groupID string, policy GroupRotationPolicy) error {
	if !oneOf(policy.Strategy, "polling", "least_connections", "p2c", "priority") {
		return fmt.Errorf("group %q rotation_policy.strategy is invalid", groupID)
	}
	if strings.TrimSpace(policy.StickyHeader) == "" {
		return fmt.Errorf("group %q rotation_policy.sticky_header is required", groupID)
	}
	if policy.CooldownDurationSeconds < 0 || policy.CooldownDurationSeconds > 86400 {
		return fmt.Errorf("group %q rotation_policy.cooldown_duration_seconds must be in range 0..86400", groupID)
	}
	if policy.MinQuotaThresholdPercent < 0 || policy.MinQuotaThresholdPercent > 1 {
		return fmt.Errorf("group %q rotation_policy.min_quota_threshold_percent must be in range 0..1", groupID)
	}
	seenCodes := map[int]bool{}
	for _, code := range policy.RotateErrorCodes {
		if code < 100 || code > 599 {
			return fmt.Errorf("group %q rotation_policy.rotate_error_codes contains invalid HTTP status %d", groupID, code)
		}
		if seenCodes[code] {
			return fmt.Errorf("group %q rotation_policy.rotate_error_codes contains duplicate HTTP status %d", groupID, code)
		}
		seenCodes[code] = true
	}
	return nil
}

func AccountPlatform(account Account) string {
	if account.Metadata != nil {
		if value, ok := account.Metadata["platform"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	typ := strings.TrimSpace(account.Type)
	switch {
	case strings.HasPrefix(typ, "openai"):
		return "openai"
	case strings.HasPrefix(typ, "anthropic"):
		return "anthropic"
	case strings.HasPrefix(typ, "gemini"):
		return "gemini"
	}
	return ""
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
	cfg.GatewayKeys = cloneGatewayKeys(cfg.GatewayKeys)
	cfg.Groups = cloneGroups(cfg.Groups)
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
		out[i].Metadata = cloneAnyMap(accounts[i].Metadata)
	}
	return out
}

func cloneGroups(groups []Group) []Group {
	out := make([]Group, len(groups))
	copy(out, groups)
	for i := range out {
		out[i].Tags = append([]string(nil), groups[i].Tags...)
		out[i].AccountIDs = append([]string(nil), groups[i].AccountIDs...)
		out[i].RotationPolicy.RotateErrorCodes = append([]int(nil), groups[i].RotationPolicy.RotateErrorCodes...)
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = cloneAny(typed[i])
		}
		return out
	default:
		return typed
	}
}

func cloneGatewayKeys(keys []GatewayKey) []GatewayKey {
	out := make([]GatewayKey, len(keys))
	copy(out, keys)
	for i := range out {
		out[i].RoutingPolicy.GroupIDs = append([]string(nil), keys[i].RoutingPolicy.GroupIDs...)
		out[i].RoutingPolicy.Tiers = append([]string(nil), keys[i].RoutingPolicy.Tiers...)
		out[i].RoutingPolicy.Tags = append([]string(nil), keys[i].RoutingPolicy.Tags...)
		out[i].RoutingPolicy.AccountIDs = append([]string(nil), keys[i].RoutingPolicy.AccountIDs...)
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

func HashGatewayKey(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func KeyPreview(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 12 {
		return maskSecret(trimmed)
	}
	return trimmed[:6] + "..." + trimmed[len(trimmed)-6:]
}

func MatchGatewayKey(cfg Config, value string) GatewayKeyMatch {
	hash := HashGatewayKey(value)
	for _, key := range cfg.GatewayKeys {
		if len(hash) != len(key.KeyHash) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(hash), []byte(key.KeyHash)) == 1 {
			return GatewayKeyMatch{Key: key, OK: key.Status == "enabled"}
		}
	}
	if cfg.GatewayKeys != nil {
		return GatewayKeyMatch{}
	}
	if strings.TrimSpace(cfg.GatewayAuth.GatewayKey) != "" && constantTimeEqual(value, cfg.GatewayAuth.GatewayKey) {
		return GatewayKeyMatch{Key: GatewayKey{ID: "legacy", Name: "Legacy Gateway Key", Preview: KeyPreview(value), Status: "enabled", RoutingPolicy: KeyRoutingPolicy{Mode: "all_enabled"}}, OK: true}
	}
	return GatewayKeyMatch{}
}

func constantTimeEqual(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (s *Store) saveLocked() error {
	return s.saveConfigLocked(s.cfg)
}

func (s *Store) reloadLocked() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	cfg := DefaultConfig()
	if err := DecodeStrict(strings.NewReader(string(data)), &cfg); err != nil {
		return err
	}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		return err
	}
	if err := Validate(cfg); err != nil {
		return err
	}
	s.cfg = cfg
	return nil
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
	if err := syncFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(dir)
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDir(dir string) error {
	if dir == "" {
		dir = "."
	}
	file, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer file.Close()
	return file.Sync()
}
