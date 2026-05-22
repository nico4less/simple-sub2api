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
