package quota

import (
	"sync"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

type Status string

const (
	StatusAvailable Status = "available"
	StatusNearLimit Status = "near_limit"
	StatusExhausted Status = "exhausted"
	StatusUnknown   Status = "unknown"
	StatusError     Status = "error"
)

type AccountQuota struct {
	AccountID     string  `json:"account_id"`
	PolicyID      string  `json:"policy_id,omitempty"`
	Status        Status  `json:"status"`
	DailyUsed     int64   `json:"daily_used_tokens"`
	DailyLimit    int64   `json:"daily_limit_tokens,omitempty"`
	WeeklyUsed    int64   `json:"weekly_used_tokens"`
	WeeklyLimit   int64   `json:"weekly_limit_tokens,omitempty"`
	UsageRatio    float64 `json:"usage_ratio"`
	SwitchBlocked bool    `json:"switch_blocked"`
	Error         string  `json:"error,omitempty"`
}

type Monitor struct {
	mu     sync.RWMutex
	cfg    config.QuotaConfig
	usage  map[string]usage
	errors map[string]string
}

type usage struct {
	daily  int64
	weekly int64
}

func NewMonitor(cfg config.QuotaConfig) *Monitor {
	return &Monitor{cfg: cfg, usage: map[string]usage{}, errors: map[string]string{}}
}

func (m *Monitor) ApplyConfig(cfg config.QuotaConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
}

func (m *Monitor) AddUsage(accountID string, tokens int64) {
	if tokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.usage[accountID]
	current.daily += tokens
	current.weekly += tokens
	m.usage[accountID] = current
}

func (m *Monitor) SetError(accountID string, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if message == "" {
		delete(m.errors, accountID)
		return
	}
	m.errors[accountID] = message
}

func (m *Monitor) State(account config.Account) AccountQuota {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateLocked(account)
}

func (m *Monitor) StateWithConfig(account config.Account, cfg config.QuotaConfig) AccountQuota {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateWithConfigLocked(account, cfg)
}

func (m *Monitor) Snapshot(accounts []config.Account) []AccountQuota {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AccountQuota, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, m.stateLocked(account))
	}
	return out
}

func (m *Monitor) stateLocked(account config.Account) AccountQuota {
	return m.stateWithConfigLocked(account, m.cfg)
}

func (m *Monitor) stateWithConfigLocked(account config.Account, cfg config.QuotaConfig) AccountQuota {
	state := AccountQuota{AccountID: account.ID, PolicyID: account.QuotaPolicy, Status: StatusUnknown}
	if message := m.errors[account.ID]; message != "" {
		state.Status = StatusError
		state.Error = message
		return state
	}
	policy, ok := findPolicy(cfg.Policies, account.QuotaPolicy)
	if !ok {
		return state
	}
	current := m.usage[account.ID]
	state.DailyUsed = current.daily
	state.WeeklyUsed = current.weekly
	state.DailyLimit = policy.DailyLimitTokens
	state.WeeklyLimit = policy.WeeklyLimitTokens
	state.UsageRatio = maxRatio(current.daily, policy.DailyLimitTokens, current.weekly, policy.WeeklyLimitTokens)
	if state.UsageRatio >= cfg.SwitchThreshold {
		state.Status = StatusExhausted
		state.SwitchBlocked = true
		return state
	}
	if state.UsageRatio >= cfg.WarnThreshold {
		state.Status = StatusNearLimit
		return state
	}
	state.Status = StatusAvailable
	return state
}

func findPolicy(policies []config.QuotaPolicy, id string) (config.QuotaPolicy, bool) {
	if id == "" {
		return config.QuotaPolicy{}, false
	}
	for _, policy := range policies {
		if policy.ID == id {
			return policy, true
		}
	}
	return config.QuotaPolicy{}, false
}

func maxRatio(dailyUsed int64, dailyLimit int64, weeklyUsed int64, weeklyLimit int64) float64 {
	max := 0.0
	if dailyLimit > 0 {
		max = float64(dailyUsed) / float64(dailyLimit)
	}
	if weeklyLimit > 0 {
		weekly := float64(weeklyUsed) / float64(weeklyLimit)
		if weekly > max {
			max = weekly
		}
	}
	return max
}
