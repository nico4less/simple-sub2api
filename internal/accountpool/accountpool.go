package accountpool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/accountcheck"
	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/quota"
	"github.com/0xForce-Network/simple-sub2api/internal/routing"
)

type AccountState struct {
	AccountID       string              `json:"account_id"`
	Label           string              `json:"label"`
	Tier            string              `json:"tier"`
	Tags            []string            `json:"tags"`
	Status          string              `json:"status"`
	Check           accountcheck.Result `json:"check"`
	Quota           quota.AccountQuota  `json:"quota"`
	CooldownUntil   string              `json:"cooldown_until,omitempty"`
	LastSelectedAt  string              `json:"last_selected_at,omitempty"`
	LastSelectedSeq uint64              `json:"last_selected_seq"`
}

type Snapshot struct {
	ConfigVersion int            `json:"config_version"`
	UpdatedAt     string         `json:"updated_at"`
	Accounts      []AccountState `json:"accounts"`
}

type Manager struct {
	mu        sync.RWMutex
	monitor   *quota.Monitor
	snapshot  Snapshot
	accounts  map[string]config.Account
	cooldowns map[string]time.Time
	rr        map[string]int
	seq       uint64
}

func NewManager(cfg config.Config) (*Manager, error) {
	m := &Manager{monitor: quota.NewMonitor(cfg.Quota), accounts: map[string]config.Account{}, cooldowns: map[string]time.Time{}, rr: map[string]int{}}
	if err := m.ApplyConfig(context.Background(), cfg); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) ApplyConfig(ctx context.Context, cfg config.Config) error {
	candidate, accounts, err := buildSnapshot(ctx, cfg, m.monitor, m.cooldowns)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monitor.ApplyConfig(cfg.Quota)
	m.snapshot = candidate
	m.accounts = accounts
	return nil
}

func (m *Manager) ValidateConfig(ctx context.Context, cfg config.Config) error {
	_, _, err := buildSnapshot(ctx, cfg, m.monitor, m.cooldowns)
	return err
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := m.snapshot
	out.Accounts = append([]AccountState(nil), m.snapshot.Accounts...)
	for i := range out.Accounts {
		out.Accounts[i].Tags = append([]string(nil), m.snapshot.Accounts[i].Tags...)
	}
	return out
}

func (m *Manager) QuotaSnapshot() []quota.AccountQuota {
	m.mu.RLock()
	defer m.mu.RUnlock()
	accounts := make([]config.Account, 0, len(m.accounts))
	for _, account := range m.accounts {
		accounts = append(accounts, account)
	}
	return m.monitor.Snapshot(accounts)
}

func (m *Manager) AddUsage(accountID string, tokens int64) (quota.AccountQuota, quota.AccountQuota, bool) {
	if tokens <= 0 {
		return quota.AccountQuota{}, quota.AccountQuota{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	account, ok := m.accounts[accountID]
	if !ok {
		return quota.AccountQuota{}, quota.AccountQuota{}, false
	}
	before := m.monitor.State(account)
	m.monitor.AddUsage(accountID, tokens)
	after := m.monitor.State(account)
	for i := range m.snapshot.Accounts {
		if m.snapshot.Accounts[i].AccountID != accountID {
			continue
		}
		m.snapshot.Accounts[i].Quota = after
		if after.Status == quota.StatusExhausted && m.snapshot.Accounts[i].Status == "healthy" {
			m.snapshot.Accounts[i].Status = "quota"
		}
		break
	}
	return before, after, crossedSwitchThreshold(before, after)
}

func crossedSwitchThreshold(before quota.AccountQuota, after quota.AccountQuota) bool {
	if !after.SwitchBlocked && after.Status != quota.StatusExhausted {
		return false
	}
	return !before.SwitchBlocked && before.Status != quota.StatusExhausted
}

func (m *Manager) Cooldown(accountID string, until time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cooldowns[accountID] = until
	for i := range m.snapshot.Accounts {
		if m.snapshot.Accounts[i].AccountID == accountID {
			m.snapshot.Accounts[i].Status = "cooldown"
			m.snapshot.Accounts[i].CooldownUntil = until.UTC().Format(time.RFC3339)
			return
		}
	}
}

func (m *Manager) Select(decision routing.Decision) (config.Account, AccountState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshCooldownsLocked(time.Now().UTC())
	eligible := make([]AccountState, 0)
	for _, tier := range append(append([]string(nil), decision.PreferTiers...), decision.FallbackTiers...) {
		for _, state := range m.snapshot.Accounts {
			if state.Tier == tier && state.Status == "healthy" {
				eligible = append(eligible, state)
			}
		}
		if len(eligible) > 0 {
			break
		}
	}
	if len(eligible) == 0 {
		return config.Account{}, AccountState{}, errors.New("no eligible account")
	}
	key := decision.TaskType + ":" + decision.MatchedRule
	index := m.rr[key] % len(eligible)
	m.rr[key] = (m.rr[key] + 1) % len(eligible)
	chosen := eligible[index]
	account := m.accounts[chosen.AccountID]
	m.seq++
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range m.snapshot.Accounts {
		if m.snapshot.Accounts[i].AccountID == chosen.AccountID {
			m.snapshot.Accounts[i].LastSelectedAt = now
			m.snapshot.Accounts[i].LastSelectedSeq = m.seq
			chosen = m.snapshot.Accounts[i]
			break
		}
	}
	return account, chosen, nil
}

func (m *Manager) refreshCooldownsLocked(now time.Time) {
	for accountID, until := range m.cooldowns {
		if until.IsZero() || until.After(now) {
			continue
		}
		delete(m.cooldowns, accountID)
		for i := range m.snapshot.Accounts {
			if m.snapshot.Accounts[i].AccountID == accountID && m.snapshot.Accounts[i].Status == "cooldown" {
				if m.snapshot.Accounts[i].Quota.Status == quota.StatusExhausted {
					m.snapshot.Accounts[i].Status = "quota"
				} else if m.snapshot.Accounts[i].Check.Status == "healthy" {
					m.snapshot.Accounts[i].Status = "healthy"
				} else {
					m.snapshot.Accounts[i].Status = "error"
				}
				m.snapshot.Accounts[i].CooldownUntil = ""
			}
		}
	}
}

func buildSnapshot(ctx context.Context, cfg config.Config, monitor *quota.Monitor, cooldowns map[string]time.Time) (Snapshot, map[string]config.Account, error) {
	if err := config.Validate(cfg); err != nil {
		return Snapshot{}, nil, err
	}
	checker := accountcheck.New(cfg.Probe)
	states := make([]AccountState, 0, len(cfg.Accounts))
	accounts := make(map[string]config.Account, len(cfg.Accounts))
	now := time.Now().UTC()
	for _, account := range cfg.Accounts {
		accounts[account.ID] = account
		check := checker.Check(ctx, cfg, account)
		if cfg.Probe.SavePolicy == "healthy_only" && account.Enabled && check.Status != "healthy" {
			return Snapshot{}, nil, errors.New("probe.save_policy=healthy_only rejected unhealthy account " + account.ID)
		}
		quotaState := monitor.StateWithConfig(account, cfg.Quota)
		state := AccountState{AccountID: account.ID, Label: account.Label, Tier: account.Tier, Tags: append([]string(nil), account.Tags...), Check: check, Quota: quotaState}
		switch {
		case !account.Enabled:
			state.Status = "disabled"
		case !cooldowns[account.ID].IsZero() && cooldowns[account.ID].After(now):
			state.Status = "cooldown"
			state.CooldownUntil = cooldowns[account.ID].UTC().Format(time.RFC3339)
		case quotaState.Status == quota.StatusExhausted:
			state.Status = "quota"
		case check.Status == "healthy":
			state.Status = "healthy"
		default:
			state.Status = "error"
		}
		states = append(states, state)
	}
	return Snapshot{ConfigVersion: cfg.ConfigVersion, UpdatedAt: now.Format(time.RFC3339), Accounts: states}, accounts, nil
}
