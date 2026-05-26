package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDefaultLoopback(t *testing.T) {
	cfg := DefaultConfig()
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate(default) error = %v", err)
	}
	if !IsLoopbackBind(cfg.Server.Bind) {
		t.Fatalf("default bind must be loopback, got %q", cfg.Server.Bind)
	}
}

func TestValidateRejectsLANWithoutExplicitModeAndPassword(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Bind = "0.0.0.0:8080"
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() accepted LAN bind without allow_lan")
	}
	cfg.Server.AllowLAN = true
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() accepted LAN bind without admin password")
	}
	cfg.Dashboard.AdminPassword = "admin-secret"
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() rejected explicit LAN mode with admin password: %v", err)
	}
}

func TestValidateRejectsGatewayKeyAsAdminPassword(t *testing.T) {
	cfg := DefaultConfig()
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	cfg.Dashboard.AdminPassword = cfg.GatewayAuth.GatewayKey
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() accepted admin password equal to gateway key")
	}
}

func TestValidateRejectsWildcardCORS(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.CORSAllowedOrigins = []string{"*"}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() accepted wildcard CORS origin")
	}
}

func TestConfigM1DefaultsCoverQueueBModules(t *testing.T) {
	cfg := DefaultConfig()
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate(default) error = %v", err)
	}
	if cfg.Routing.DefaultTaskType != "default" {
		t.Fatalf("routing default task type = %q", cfg.Routing.DefaultTaskType)
	}
	if cfg.Probe.TimeoutSeconds == 0 || cfg.Metrics.RecentErrorsLimit == 0 {
		t.Fatal("probe and metrics defaults must be populated")
	}
}

func TestValidateRejectsInvalidAccountSchema(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Accounts = []Account{{
		ID:         "acct_1",
		Type:       "openai_api_key",
		Label:      "Broken account",
		Tier:       "simple",
		Credential: "",
		Enabled:    true,
	}}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate() accepted account without credential")
	}
}

func TestValidateRejectsMixedSubscriptionFamiliesInGroup(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Accounts = []Account{
		{ID: "openai_acct", Type: "openai_api_key", Label: "OpenAI", Tier: "simple", Credential: "api_key=sk-openai", Metadata: map[string]any{"platform": "openai"}, Enabled: true},
		{ID: "claude_acct", Type: "oauth", Label: "Claude", Tier: "simple", Credential: "refresh_token=claude", Metadata: map[string]any{"platform": "anthropic"}, Enabled: true},
	}
	cfg.Groups = []Group{{ID: "openai", Name: "OpenAI", Platform: "openai", Status: "active", AccountIDs: []string{"openai_acct", "claude_acct"}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z"}}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "cannot include") {
		t.Fatalf("Validate() error = %v, want mixed platform rejection", err)
	}
}

func TestValidateAcceptsSingleSubscriptionFamilyGroup(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Accounts = []Account{{ID: "claude_acct", Type: "oauth", Label: "Claude", Tier: "simple", Credential: "refresh_token=claude", Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}
	cfg.Groups = []Group{{ID: "claude", Name: "Claude", Platform: "anthropic", Status: "active", AccountIDs: []string{"claude_acct"}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z"}}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() rejected single-family group: %v", err)
	}
}

func TestGroupRotationPolicyDefaultsAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Accounts = []Account{{ID: "acct_1", Type: "openai_api_key", Label: "OpenAI", Tier: "simple", Credential: "api_key=sk-test", Enabled: true}}
	cfg.Groups = []Group{{ID: "openai", Name: "OpenAI", Platform: "openai", Status: "active", AccountIDs: []string{"acct_1"}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z"}}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	policy := cfg.Groups[0].RotationPolicy
	if policy.Strategy != "polling" || policy.StickyHeader != "X-Session-ID" || !policy.RetryOnErrors || policy.CooldownDurationSeconds != 60 || policy.MinQuotaThresholdPercent != 0.10 || len(policy.RotateErrorCodes) != 5 {
		t.Fatalf("rotation defaults = %#v", policy)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() rejected default rotation policy: %v", err)
	}
	cfg.Groups[0].RotationPolicy.Strategy = "invalid"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "rotation_policy.strategy") {
		t.Fatalf("Validate() error = %v, want invalid strategy rejection", err)
	}
}

func TestGroupRotationPolicyValidationBoundaries(t *testing.T) {
	base := DefaultConfig()
	base.Accounts = []Account{{ID: "acct_1", Type: "openai_api_key", Label: "OpenAI", Tier: "simple", Credential: "api_key=sk-test", Enabled: true}}
	base.Groups = []Group{{ID: "openai", Name: "OpenAI", Platform: "openai", Status: "active", AccountIDs: []string{"acct_1"}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z", RotationPolicy: GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}}}
	if err := EnsureDefaultsAndSecrets(&base); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	for _, cooldown := range []int{0, 86400} {
		cfg := base
		cfg.Groups = cloneGroups(base.Groups)
		cfg.Groups[0].RotationPolicy.CooldownDurationSeconds = cooldown
		if err := Validate(cfg); err != nil {
			t.Fatalf("Validate() rejected cooldown boundary %d: %v", cooldown, err)
		}
	}
	for _, threshold := range []float64{0, 1} {
		cfg := base
		cfg.Groups = cloneGroups(base.Groups)
		cfg.Groups[0].RotationPolicy.MinQuotaThresholdPercent = threshold
		if err := Validate(cfg); err != nil {
			t.Fatalf("Validate() rejected quota threshold boundary %v: %v", threshold, err)
		}
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "cooldown too high", mutate: func(cfg *Config) { cfg.Groups[0].RotationPolicy.CooldownDurationSeconds = 86401 }, want: "cooldown_duration_seconds"},
		{name: "threshold below zero", mutate: func(cfg *Config) { cfg.Groups[0].RotationPolicy.MinQuotaThresholdPercent = -0.01 }, want: "min_quota_threshold_percent"},
		{name: "threshold above one", mutate: func(cfg *Config) { cfg.Groups[0].RotationPolicy.MinQuotaThresholdPercent = 1.01 }, want: "min_quota_threshold_percent"},
		{name: "status too low", mutate: func(cfg *Config) { cfg.Groups[0].RotationPolicy.RotateErrorCodes = []int{99} }, want: "invalid HTTP status"},
		{name: "status too high", mutate: func(cfg *Config) { cfg.Groups[0].RotationPolicy.RotateErrorCodes = []int{600} }, want: "invalid HTTP status"},
		{name: "duplicate status", mutate: func(cfg *Config) { cfg.Groups[0].RotationPolicy.RotateErrorCodes = []int{429, 429} }, want: "duplicate HTTP status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.Groups = cloneGroups(base.Groups)
			tt.mutate(&cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTunnelConfigDefaultsAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tunnel = TunnelConfig{}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if cfg.Tunnel.Mode != "quick" || cfg.Tunnel.LogLimitLines != 100 {
		t.Fatalf("tunnel defaults = %#v", cfg.Tunnel)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() rejected default tunnel config: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "invalid mode", mutate: func(cfg *Config) { cfg.Tunnel.Mode = "invalid" }, want: "tunnel.mode"},
		{name: "log too low", mutate: func(cfg *Config) { cfg.Tunnel.LogLimitLines = 9 }, want: "tunnel.log_limit_lines"},
		{name: "log too high", mutate: func(cfg *Config) { cfg.Tunnel.LogLimitLines = 1001 }, want: "tunnel.log_limit_lines"},
		{name: "enabled named missing token", mutate: func(cfg *Config) { cfg.Tunnel.Enabled = true; cfg.Tunnel.Mode = "named" }, want: "tunnel.token"},
		{name: "malformed token", mutate: func(cfg *Config) { cfg.Tunnel.Mode = "named"; cfg.Tunnel.Token = "short token" }, want: "tunnel.token"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cfg
			tt.mutate(&candidate)
			if err := Validate(candidate); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}

	cfg.Tunnel = TunnelConfig{Enabled: true, Mode: "named", Token: "01234567-89ab-cdef-0123-456789abcdef", LogLimitLines: 10}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() rejected named tunnel boundary config: %v", err)
	}
	cfg.Tunnel.LogLimitLines = 1000
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() rejected high log boundary config: %v", err)
	}
}

func TestTunnelConfigDecodeUpdateAndRedaction(t *testing.T) {
	store, err := NewMemoryStore(DefaultConfig())
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	updated, err := store.Update(1, func(cfg *Config) error {
		cfg.Tunnel = TunnelConfig{Enabled: true, Mode: "named", Token: "01234567-89ab-cdef-0123-456789abcdef", BinaryPath: " /usr/local/bin/cloudflared ", LogLimitLines: 250}
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ConfigVersion != 2 || !updated.Tunnel.Enabled || updated.Tunnel.Mode != "named" || updated.Tunnel.LogLimitLines != 250 {
		t.Fatalf("updated tunnel config = %#v", updated.Tunnel)
	}
	if updated.Tunnel.BinaryPath != "/usr/local/bin/cloudflared" {
		t.Fatalf("binary path was not normalized: %q", updated.Tunnel.BinaryPath)
	}
	redacted := Redacted(updated)
	if redacted.Tunnel.Token == updated.Tunnel.Token || !strings.Contains(redacted.Tunnel.Token, "...") {
		t.Fatalf("tunnel token was not redacted: %q", redacted.Tunnel.Token)
	}

	decoded := DefaultConfig()
	if err := DecodeStrict(strings.NewReader(`{"config_version":1,"server":{"bind":"127.0.0.1:8080","allow_lan":false,"cors_allowed_origins":[]},"gateway_auth":{"gateway_key":"s2a_123456789012345678901234"},"dashboard":{"session_ttl_seconds":28800},"tunnel":{"enabled":true,"mode":"quick","log_limit_lines":100}}`), &decoded); err != nil {
		t.Fatalf("DecodeStrict() rejected tunnel config: %v", err)
	}
	if err := EnsureDefaultsAndSecrets(&decoded); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets(decoded) error = %v", err)
	}
	if err := Validate(decoded); err != nil {
		t.Fatalf("Validate(decoded tunnel config) error = %v", err)
	}
}

func TestValidateNormalizesSocks5ProxyToSocks5H(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Proxies = []ProxyConfig{{ID: "proxy_1", URL: "socks5://127.0.0.1:1080"}}
	if err := EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if got := cfg.Proxies[0].URL; got != "socks5h://127.0.0.1:1080" {
		t.Fatalf("proxy URL = %q", got)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	cfg := DefaultConfig()
	if err := DecodeStrict(strings.NewReader(`{"config_version":1,"server":{"bind":"127.0.0.1:8080","allow_lan":false,"cors_allowed_origins":[]},"unknown":true}`), &cfg); err == nil {
		t.Fatal("DecodeStrict() accepted unknown field")
	}
}

func TestStoreUpdateEnforcesConfigVersionConflict(t *testing.T) {
	store, err := NewMemoryStore(DefaultConfig())
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	_, err = store.Update(999, func(cfg *Config) error { return nil })
	if !errors.Is(err, ErrConfigVersionConflict) {
		t.Fatalf("Update() error = %v, want ErrConfigVersionConflict", err)
	}
}

func TestFileStoreUpdateReloadsDiskVersionBeforeMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "simple.json")
	store, created, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !created {
		t.Fatal("Open() did not create fresh config")
	}
	disk := store.Snapshot()
	disk.ConfigVersion = 7
	disk.Accounts = []Account{{ID: "acct_disk", Type: "openai_api_key", Label: "Disk", Tier: "simple", Credential: "api_key=sk-disk", Enabled: true}}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		t.Fatalf("marshal disk config: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write disk config: %v", err)
	}
	_, err = store.Update(1, func(cfg *Config) error { return nil })
	if !errors.Is(err, ErrConfigVersionConflict) {
		t.Fatalf("Update() error = %v, want ErrConfigVersionConflict", err)
	}
	updated, err := store.Update(7, func(cfg *Config) error {
		cfg.Accounts[0].Label = "Disk Updated"
		return nil
	})
	if err != nil {
		t.Fatalf("Update() after reload error = %v", err)
	}
	if updated.ConfigVersion != 8 || updated.Accounts[0].Label != "Disk Updated" {
		t.Fatalf("updated config = %#v", updated)
	}
}
