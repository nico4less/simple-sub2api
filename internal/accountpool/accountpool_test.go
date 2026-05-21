package accountpool_test

import (
	"context"
	"testing"

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
