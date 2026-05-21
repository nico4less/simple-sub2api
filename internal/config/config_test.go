package config

import (
	"errors"
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
