package accountpool_test

import (
	"context"
	"testing"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/accountpool"
	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/routing"
)

func TestManagerSelectsHealthyRoundRobinAndKeepsOldPoolOnInvalidConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Accounts = []config.Account{
		{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-one", Enabled: true},
		{ID: "acct_2", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "api_key=sk-two", Enabled: true},
	}
	if err := config.EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	manager, err := accountpool.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	decision := routing.Decision{TaskType: "document", PreferTiers: []string{"simple"}, MatchedRule: "test"}
	first, _, err := manager.Select(decision)
	if err != nil {
		t.Fatalf("Select() first error = %v", err)
	}
	second, _, err := manager.Select(decision)
	if err != nil {
		t.Fatalf("Select() second error = %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("round-robin did not advance: %s then %s", first.ID, second.ID)
	}

	bad := cfg
	bad.Accounts[0].ProxyRef = "missing_proxy"
	if err := manager.ApplyConfig(context.Background(), bad); err == nil {
		t.Fatal("ApplyConfig() accepted invalid proxy reference")
	}
	snapshot := manager.Snapshot()
	if len(snapshot.Accounts) != 2 || snapshot.Accounts[0].AccountID != "acct_1" {
		t.Fatalf("old pool was not preserved: %#v", snapshot)
	}
}

func TestManagerSelectWithGroupPolicyStickyLeastConnectionsAndQuotaGuard(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Accounts = []config.Account{
		{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-one", QuotaPolicy: "daily", Tags: []string{"openai"}, Enabled: true},
		{ID: "acct_2", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "api_key=sk-two", QuotaPolicy: "daily", Tags: []string{"openai"}, Enabled: true},
	}
	cfg.Quota.Policies = []config.QuotaPolicy{{ID: "daily", DailyLimitTokens: 100, Source: "test"}}
	cfg.Groups = []config.Group{{
		ID:         "openai",
		Name:       "OpenAI",
		Platform:   "openai",
		Status:     "active",
		AccountIDs: []string{"acct_1", "acct_2"},
		CreatedAt:  "1970-01-01T00:00:00Z",
		UpdatedAt:  "1970-01-01T00:00:00Z",
		RotationPolicy: config.GroupRotationPolicy{
			Strategy:                 "least_connections",
			StickySessionsEnabled:    true,
			StickyHeader:             "X-Session-ID",
			RetryOnErrors:            true,
			RotateErrorCodes:         []int{429},
			CooldownDurationSeconds:  60,
			EnableQuotaProtection:    true,
			MinQuotaThresholdPercent: 0.20,
		},
	}}
	if err := config.EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	manager, err := accountpool.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	decision := routing.Decision{TaskType: "document", PreferTiers: []string{"simple"}, MatchedRule: "test"}
	group := cfg.Groups[0]
	manager.IncrementActiveConn("acct_1")
	selected, _, err := manager.SelectWithPolicyOptions(decision, config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, &group, accountpool.SelectOptions{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("SelectWithPolicyOptions() error = %v", err)
	}
	if selected.ID != "acct_2" {
		t.Fatalf("least-connections selected %q, want acct_2", selected.ID)
	}
	manager.IncrementActiveConn("acct_2")
	manager.IncrementActiveConn("acct_2")
	sticky, _, err := manager.SelectWithPolicyOptions(decision, config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, &group, accountpool.SelectOptions{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("sticky SelectWithPolicyOptions() error = %v", err)
	}
	if sticky.ID != "acct_2" {
		t.Fatalf("sticky session selected %q, want acct_2", sticky.ID)
	}
	manager.AddUsage("acct_2", 90)
	guarded, _, err := manager.SelectWithPolicyOptions(decision, config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, &group, accountpool.SelectOptions{SessionID: "session-b"})
	if err != nil {
		t.Fatalf("quota guarded SelectWithPolicyOptions() error = %v", err)
	}
	if guarded.ID != "acct_1" {
		t.Fatalf("quota guard selected %q, want acct_1", guarded.ID)
	}
}

func TestManagerSelectWithGroupPolicyExcludesCooldownStickyAccount(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Accounts = []config.Account{
		{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-one", Tags: []string{"openai"}, Enabled: true},
		{ID: "acct_2", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "api_key=sk-two", Tags: []string{"openai"}, Enabled: true},
	}
	cfg.Groups = []config.Group{{ID: "openai", Name: "OpenAI", Platform: "openai", Status: "active", AccountIDs: []string{"acct_1", "acct_2"}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z", RotationPolicy: config.GroupRotationPolicy{Strategy: "polling", StickySessionsEnabled: true, StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}}}
	if err := config.EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	manager, err := accountpool.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	decision := routing.Decision{TaskType: "document", PreferTiers: []string{"simple"}, MatchedRule: "test"}
	group := cfg.Groups[0]
	first, _, err := manager.SelectWithPolicyOptions(decision, config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, &group, accountpool.SelectOptions{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("first SelectWithPolicyOptions() error = %v", err)
	}
	manager.Cooldown(first.ID, time.Now().UTC().Add(time.Minute))
	second, _, err := manager.SelectWithPolicyOptions(decision, config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, &group, accountpool.SelectOptions{SessionID: "session-a"})
	if err != nil {
		t.Fatalf("second SelectWithPolicyOptions() error = %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("cooldown sticky account was reused: %q", second.ID)
	}
}

func TestManagerSelectWithGroupPolicyPriorityAndP2C(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Accounts = []config.Account{
		{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-one", Enabled: true},
		{ID: "acct_2", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "api_key=sk-two", Enabled: true},
		{ID: "acct_3", Type: "openai_api_key", Label: "C", Tier: "simple", Credential: "api_key=sk-three", Enabled: true},
	}
	cfg.Groups = []config.Group{{ID: "openai", Name: "OpenAI", Platform: "openai", Status: "active", AccountIDs: []string{"acct_2", "acct_1", "acct_3"}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z", RotationPolicy: config.GroupRotationPolicy{Strategy: "priority", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}}}
	if err := config.EnsureDefaultsAndSecrets(&cfg); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	manager, err := accountpool.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	decision := routing.Decision{TaskType: "document", PreferTiers: []string{"simple"}, MatchedRule: "test"}
	group := cfg.Groups[0]
	priority, _, err := manager.SelectWithPolicyOptions(decision, config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, &group, accountpool.SelectOptions{})
	if err != nil {
		t.Fatalf("priority SelectWithPolicyOptions() error = %v", err)
	}
	if priority.ID != "acct_2" {
		t.Fatalf("priority selected %q, want acct_2", priority.ID)
	}
	group.RotationPolicy.Strategy = "p2c"
	manager.IncrementActiveConn("acct_1")
	manager.IncrementActiveConn("acct_1")
	manager.IncrementActiveConn("acct_2")
	p2c, _, err := manager.SelectWithPolicyOptions(decision, config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, &group, accountpool.SelectOptions{})
	if err != nil {
		t.Fatalf("p2c SelectWithPolicyOptions() error = %v", err)
	}
	if p2c.ID == "" {
		t.Fatal("p2c selected empty account")
	}
}
