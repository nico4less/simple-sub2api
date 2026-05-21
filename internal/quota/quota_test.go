package quota_test

import (
	"testing"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/quota"
)

func TestMonitorStates(t *testing.T) {
	cfg := config.DefaultConfig().Quota
	cfg.Policies = []config.QuotaPolicy{{ID: "daily", DailyLimitTokens: 100, Source: "local"}}
	monitor := quota.NewMonitor(cfg)
	account := config.Account{ID: "acct_1", QuotaPolicy: "daily"}
	if got := monitor.State(account).Status; got != quota.StatusAvailable {
		t.Fatalf("initial status = %s", got)
	}
	monitor.AddUsage("acct_1", 85)
	if got := monitor.State(account).Status; got != quota.StatusNearLimit {
		t.Fatalf("near-limit status = %s", got)
	}
	monitor.AddUsage("acct_1", 10)
	state := monitor.State(account)
	if state.Status != quota.StatusExhausted || !state.SwitchBlocked {
		t.Fatalf("exhausted state = %#v", state)
	}
}
