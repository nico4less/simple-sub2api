package accountpool

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand"
	"strings"
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
	ActiveConns     int                 `json:"active_conns"`
	LastSelectedAt  string              `json:"last_selected_at,omitempty"`
	LastSelectedSeq uint64              `json:"last_selected_seq"`
}

type SelectOptions struct {
	SessionID string
	Excluded  map[string]bool
}

type Snapshot struct {
	ConfigVersion int            `json:"config_version"`
	UpdatedAt     string         `json:"updated_at"`
	Accounts      []AccountState `json:"accounts"`
}

type Manager struct {
	mu             sync.RWMutex
	monitor        *quota.Monitor
	snapshot       Snapshot
	accounts       map[string]config.Account
	cooldowns      map[string]time.Time
	rr             map[string]int
	seq            uint64
	activeConns    map[string]int
	stickySessions map[string]stickyEntry
	failures       map[string]int
}

type stickyEntry struct {
	AccountID string
	ExpiresAt time.Time
}

func NewManager(cfg config.Config) (*Manager, error) {
	m := &Manager{monitor: quota.NewMonitor(cfg.Quota), accounts: map[string]config.Account{}, cooldowns: map[string]time.Time{}, rr: map[string]int{}, activeConns: map[string]int{}, stickySessions: map[string]stickyEntry{}, failures: map[string]int{}}
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
	m.pruneRuntimeStateLocked(accounts)
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
		out.Accounts[i].ActiveConns = m.activeConns[out.Accounts[i].AccountID]
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
	m.deleteStickyForAccountLocked(accountID)
	for i := range m.snapshot.Accounts {
		if m.snapshot.Accounts[i].AccountID == accountID {
			m.snapshot.Accounts[i].Status = "cooldown"
			m.snapshot.Accounts[i].CooldownUntil = until.UTC().Format(time.RFC3339)
			return
		}
	}
}

func (m *Manager) IncrementActiveConn(accountID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accountID == "" {
		return
	}
	m.activeConns[accountID]++
	m.setSnapshotActiveConnsLocked(accountID)
}

func (m *Manager) DecrementActiveConn(accountID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accountID == "" {
		return
	}
	if m.activeConns[accountID] <= 1 {
		delete(m.activeConns, accountID)
	} else {
		m.activeConns[accountID]--
	}
	m.setSnapshotActiveConnsLocked(accountID)
}

func (m *Manager) MarkSuccess(accountID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accountID == "" {
		return
	}
	delete(m.failures, accountID)
	m.monitor.SetError(accountID, "")
}

func (m *Manager) RecordFailure(accountID string, permanent bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accountID == "" || !permanent {
		return 0
	}
	m.failures[accountID]++
	return m.failures[accountID]
}

func (m *Manager) DisableAccount(accountID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accountID == "" {
		return
	}
	delete(m.failures, accountID)
	delete(m.activeConns, accountID)
	delete(m.cooldowns, accountID)
	m.deleteStickyForAccountLocked(accountID)
	for i := range m.snapshot.Accounts {
		if m.snapshot.Accounts[i].AccountID == accountID {
			m.snapshot.Accounts[i].Status = "disabled"
			m.snapshot.Accounts[i].CooldownUntil = ""
			m.snapshot.Accounts[i].ActiveConns = 0
			return
		}
	}
}

func (m *Manager) Select(decision routing.Decision) (config.Account, AccountState, error) {
	return m.SelectWithPolicy(decision, config.KeyRoutingPolicy{Mode: "all_enabled"})
}

func (m *Manager) SelectWithPolicy(decision routing.Decision, policy config.KeyRoutingPolicy) (config.Account, AccountState, error) {
	return m.SelectWithPolicyOptions(decision, policy, nil, SelectOptions{})
}

func (m *Manager) SelectWithPolicyOptions(decision routing.Decision, policy config.KeyRoutingPolicy, group *config.Group, options SelectOptions) (config.Account, AccountState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.refreshCooldownsLocked(now)
	m.pruneExpiredStickyLocked(now)
	groupPolicy := config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RotateErrorCodes: []int{429, 401, 403, 404, 500}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.10}
	groupID := ""
	groupAccountOrder := []string(nil)
	if group != nil {
		groupID = group.ID
		groupAccountOrder = append([]string(nil), group.AccountIDs...)
		groupPolicy = group.RotationPolicy
	}
	eligible := make([]AccountState, 0)
	tiers := append(append([]string(nil), decision.PreferTiers...), decision.FallbackTiers...)
	if len(policy.Tiers) > 0 {
		tiers = append([]string(nil), policy.Tiers...)
	}
	for _, tier := range tiers {
		for _, state := range m.snapshot.Accounts {
			if state.Tier == tier && state.Status == "healthy" && matchesKeyPolicy(state, policy, group) && matchesGroup(state, group) && !options.Excluded[state.AccountID] {
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
	if groupPolicy.EnableQuotaProtection {
		eligible = preferNonProtectedQuota(eligible, groupPolicy.MinQuotaThresholdPercent)
	}
	stickyKey := stickyKey(groupID, options.SessionID)
	if groupPolicy.StickySessionsEnabled && stickyKey != "" {
		if entry, ok := m.stickySessions[stickyKey]; ok && entry.ExpiresAt.After(now) {
			for _, state := range eligible {
				if state.AccountID == entry.AccountID {
					return m.finishSelectionLocked(state)
				}
			}
			delete(m.stickySessions, stickyKey)
		}
	}
	key := decision.TaskType + ":" + decision.MatchedRule + ":" + groupID
	chosen := m.chooseLocked(eligible, groupPolicy.Strategy, key, groupAccountOrder)
	if groupPolicy.StickySessionsEnabled && stickyKey != "" {
		m.stickySessions[stickyKey] = stickyEntry{AccountID: chosen.AccountID, ExpiresAt: now.Add(20 * time.Minute)}
	}
	return m.finishSelectionLocked(chosen)
}

func (m *Manager) finishSelectionLocked(chosen AccountState) (config.Account, AccountState, error) {
	account := m.accounts[chosen.AccountID]
	m.seq++
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range m.snapshot.Accounts {
		if m.snapshot.Accounts[i].AccountID == chosen.AccountID {
			m.snapshot.Accounts[i].LastSelectedAt = now
			m.snapshot.Accounts[i].LastSelectedSeq = m.seq
			m.snapshot.Accounts[i].ActiveConns = m.activeConns[chosen.AccountID]
			chosen = m.snapshot.Accounts[i]
			break
		}
	}
	return account, chosen, nil
}

func matchesKeyPolicy(state AccountState, policy config.KeyRoutingPolicy, group *config.Group) bool {
	mode := policy.Mode
	if mode == "" {
		mode = "all_enabled"
	}
	switch mode {
	case "all_enabled", "tier_preference":
		return true
	case "tags":
		if len(policy.Tags) == 0 {
			return true
		}
		return hasAnyTag(state.Tags, policy.Tags)
	case "groups":
		if len(policy.GroupIDs) == 0 {
			return true
		}
		if group != nil {
			for _, groupID := range policy.GroupIDs {
				if groupID == group.ID {
					return matchesGroup(state, group)
				}
			}
		}
		return hasAnyTag(state.Tags, policy.GroupIDs)
	case "account_ids":
		if len(policy.AccountIDs) == 0 {
			return true
		}
		for _, id := range policy.AccountIDs {
			if id == state.AccountID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func matchesGroup(state AccountState, group *config.Group) bool {
	if group == nil {
		return true
	}
	if group.Status != "active" {
		return false
	}
	for _, id := range group.AccountIDs {
		if id == state.AccountID {
			return true
		}
	}
	return false
}

func (m *Manager) chooseLocked(eligible []AccountState, strategy string, key string, groupAccountOrder []string) AccountState {
	switch strategy {
	case "least_connections":
		return m.chooseLeastConnectionsLocked(eligible, key)
	case "p2c":
		return m.chooseP2CLocked(eligible, key)
	case "priority":
		return choosePriority(eligible, groupAccountOrder)
	default:
		return m.choosePollingLocked(eligible, key)
	}
}

func (m *Manager) choosePollingLocked(eligible []AccountState, key string) AccountState {
	index := m.rr[key] % len(eligible)
	m.rr[key] = (m.rr[key] + 1) % len(eligible)
	return eligible[index]
}

func (m *Manager) chooseLeastConnectionsLocked(eligible []AccountState, key string) AccountState {
	bestConns := m.activeConns[eligible[0].AccountID]
	candidates := make([]AccountState, 0, len(eligible))
	for _, state := range eligible {
		conns := m.activeConns[state.AccountID]
		if conns < bestConns {
			bestConns = conns
			candidates = candidates[:0]
		}
		if conns == bestConns {
			candidates = append(candidates, state)
		}
	}
	return m.choosePollingLocked(candidates, key+":least")
}

func (m *Manager) chooseP2CLocked(eligible []AccountState, key string) AccountState {
	if len(eligible) <= 2 {
		return m.chooseLeastConnectionsLocked(eligible, key+":p2c-small")
	}
	rng := rand.New(rand.NewSource(int64(hashKey(key, m.seq))))
	first := rng.Intn(len(eligible))
	second := rng.Intn(len(eligible) - 1)
	if second >= first {
		second++
	}
	a := eligible[first]
	b := eligible[second]
	aConns := m.activeConns[a.AccountID]
	bConns := m.activeConns[b.AccountID]
	if aConns < bConns {
		return a
	}
	if bConns < aConns {
		return b
	}
	if m.rr[key+":p2c-tie"]%2 == 0 {
		m.rr[key+":p2c-tie"]++
		return a
	}
	m.rr[key+":p2c-tie"]++
	return b
}

func choosePriority(eligible []AccountState, groupAccountOrder []string) AccountState {
	byID := map[string]AccountState{}
	for _, state := range eligible {
		byID[state.AccountID] = state
	}
	for _, id := range groupAccountOrder {
		if state, ok := byID[id]; ok {
			return state
		}
	}
	return eligible[0]
}

func preferNonProtectedQuota(eligible []AccountState, minRemainingRatio float64) []AccountState {
	preferred := make([]AccountState, 0, len(eligible))
	protected := make([]AccountState, 0, len(eligible))
	for _, state := range eligible {
		remaining := 1 - state.Quota.UsageRatio
		if state.Quota.Status != quota.StatusUnknown && remaining < minRemainingRatio {
			protected = append(protected, state)
			continue
		}
		preferred = append(preferred, state)
	}
	if len(preferred) > 0 {
		return preferred
	}
	return protected
}

func stickyKey(groupID string, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return groupID + ":" + sessionID
}

func hashKey(key string, seq uint64) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64() + seq + uint64(time.Now().UnixNano())
}

func hasAnyTag(accountTags []string, policyTags []string) bool {
	seen := map[string]bool{}
	for _, tag := range accountTags {
		seen[strings.ToLower(strings.TrimSpace(tag))] = true
	}
	for _, tag := range policyTags {
		if seen[strings.ToLower(strings.TrimSpace(tag))] {
			return true
		}
	}
	return false
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

func (m *Manager) pruneExpiredStickyLocked(now time.Time) {
	for key, entry := range m.stickySessions {
		if entry.ExpiresAt.IsZero() || !entry.ExpiresAt.After(now) {
			delete(m.stickySessions, key)
		}
	}
}

func (m *Manager) deleteStickyForAccountLocked(accountID string) {
	for key, entry := range m.stickySessions {
		if entry.AccountID == accountID {
			delete(m.stickySessions, key)
		}
	}
}

func (m *Manager) setSnapshotActiveConnsLocked(accountID string) {
	for i := range m.snapshot.Accounts {
		if m.snapshot.Accounts[i].AccountID == accountID {
			m.snapshot.Accounts[i].ActiveConns = m.activeConns[accountID]
			return
		}
	}
}

func (m *Manager) pruneRuntimeStateLocked(accounts map[string]config.Account) {
	for accountID := range m.activeConns {
		if _, ok := accounts[accountID]; !ok {
			delete(m.activeConns, accountID)
		}
	}
	for accountID := range m.failures {
		if _, ok := accounts[accountID]; !ok {
			delete(m.failures, accountID)
		}
	}
	for key, entry := range m.stickySessions {
		if _, ok := accounts[entry.AccountID]; !ok {
			delete(m.stickySessions, key)
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
