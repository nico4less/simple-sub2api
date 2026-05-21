package metrics

import (
	"strings"
	"sync"
	"time"
)

const defaultRecentErrorsLimit = 100

type Recorder struct {
	mu                sync.RWMutex
	startedAt         time.Time
	recentErrorsLimit int
	totalRequests     uint64
	successRequests   uint64
	errorRequests     uint64
	perAccountHits    map[string]uint64
	perAccountErrors  map[string]uint64
	cooldownCounts    map[string]uint64
	routingDecisions  []RoutingDecision
	quotaSwitchCounts map[string]uint64
	recentErrors      []RecentError
}

type RequestResult struct {
	AccountID  string
	StatusCode int
	Success    bool
	Error      string
}

type RoutingDecision struct {
	At              string   `json:"at"`
	TaskType        string   `json:"task_type"`
	MatchedRule     string   `json:"matched_rule"`
	PreferTiers     []string `json:"prefer_tiers"`
	FallbackTiers   []string `json:"fallback_tiers"`
	SelectedTier    string   `json:"selected_tier,omitempty"`
	SelectedAccount string   `json:"selected_account,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

type RecentError struct {
	At         string `json:"at"`
	AccountID  string `json:"account_id,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	Message    string `json:"message"`
}

type Snapshot struct {
	StartedAt         string            `json:"started_at"`
	UptimeSeconds     int64             `json:"uptime_seconds"`
	QPS               float64           `json:"qps"`
	TotalRequests     uint64            `json:"total_requests"`
	SuccessRequests   uint64            `json:"success_requests"`
	ErrorRequests     uint64            `json:"error_requests"`
	SuccessRate       float64           `json:"success_rate"`
	PerAccountHits    map[string]uint64 `json:"per_account_hits"`
	PerAccountErrors  map[string]uint64 `json:"per_account_errors"`
	CooldownCounts    map[string]uint64 `json:"cooldown_counts"`
	QuotaSwitchCounts map[string]uint64 `json:"quota_switch_counts"`
	RoutingDecisions  []RoutingDecision `json:"routing_decisions"`
	RecentErrors      []RecentError     `json:"recent_errors"`
}

func NewRecorder(limit int) *Recorder {
	if limit <= 0 {
		limit = defaultRecentErrorsLimit
	}
	return &Recorder{
		startedAt:         time.Now().UTC(),
		recentErrorsLimit: limit,
		perAccountHits:    map[string]uint64{},
		perAccountErrors:  map[string]uint64{},
		cooldownCounts:    map[string]uint64{},
		quotaSwitchCounts: map[string]uint64{},
	}
}

func (r *Recorder) ApplyRecentErrorsLimit(limit int) {
	if r == nil {
		return
	}
	if limit <= 0 {
		limit = defaultRecentErrorsLimit
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recentErrorsLimit = limit
	r.trimRecentErrorsLocked()
}

func (r *Recorder) RecordRequest(result RequestResult) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.totalRequests++
	accountID := strings.TrimSpace(result.AccountID)
	if result.Success {
		r.successRequests++
		if accountID != "" {
			r.perAccountHits[accountID]++
		}
		return
	}
	r.errorRequests++
	if accountID != "" {
		r.perAccountErrors[accountID]++
	}
	message := sanitizeMessage(result.Error)
	if message == "" {
		message = "request failed"
	}
	r.appendRecentErrorLocked(RecentError{At: time.Now().UTC().Format(time.RFC3339), AccountID: accountID, StatusCode: result.StatusCode, Message: message})
}

func (r *Recorder) RecordRoutingDecision(decision RoutingDecision) {
	if r == nil {
		return
	}
	decision.At = time.Now().UTC().Format(time.RFC3339)
	decision.TaskType = sanitizeMessage(decision.TaskType)
	decision.MatchedRule = sanitizeMessage(decision.MatchedRule)
	decision.SelectedTier = sanitizeMessage(decision.SelectedTier)
	decision.SelectedAccount = sanitizeMessage(decision.SelectedAccount)
	decision.Reason = sanitizeMessage(decision.Reason)
	decision.PreferTiers = sanitizeList(decision.PreferTiers)
	decision.FallbackTiers = sanitizeList(decision.FallbackTiers)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routingDecisions = append(r.routingDecisions, decision)
	if len(r.routingDecisions) > r.recentErrorsLimit {
		r.routingDecisions = append([]RoutingDecision(nil), r.routingDecisions[len(r.routingDecisions)-r.recentErrorsLimit:]...)
	}
}

func (r *Recorder) RecordCooldown(accountID string) {
	if r == nil {
		return
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cooldownCounts[accountID]++
}

func (r *Recorder) RecordQuotaSwitch(accountID string) {
	if r == nil {
		return
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.quotaSwitchCounts[accountID]++
}

func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return NewRecorder(defaultRecentErrorsLimit).Snapshot()
	}
	now := time.Now().UTC()
	r.mu.RLock()
	defer r.mu.RUnlock()
	uptime := now.Sub(r.startedAt).Seconds()
	qps := float64(0)
	if uptime > 0 {
		qps = float64(r.totalRequests) / uptime
	}
	successRate := float64(0)
	if r.totalRequests > 0 {
		successRate = float64(r.successRequests) / float64(r.totalRequests)
	}
	return Snapshot{
		StartedAt:         r.startedAt.Format(time.RFC3339),
		UptimeSeconds:     int64(uptime),
		QPS:               qps,
		TotalRequests:     r.totalRequests,
		SuccessRequests:   r.successRequests,
		ErrorRequests:     r.errorRequests,
		SuccessRate:       successRate,
		PerAccountHits:    cloneCounter(r.perAccountHits),
		PerAccountErrors:  cloneCounter(r.perAccountErrors),
		CooldownCounts:    cloneCounter(r.cooldownCounts),
		QuotaSwitchCounts: cloneCounter(r.quotaSwitchCounts),
		RoutingDecisions:  append([]RoutingDecision(nil), r.routingDecisions...),
		RecentErrors:      append([]RecentError(nil), r.recentErrors...),
	}
}

func (r *Recorder) appendRecentErrorLocked(item RecentError) {
	item.Message = sanitizeMessage(item.Message)
	r.recentErrors = append(r.recentErrors, item)
	r.trimRecentErrorsLocked()
}

func (r *Recorder) trimRecentErrorsLocked() {
	if r.recentErrorsLimit <= 0 {
		r.recentErrorsLimit = defaultRecentErrorsLimit
	}
	if len(r.recentErrors) > r.recentErrorsLimit {
		r.recentErrors = append([]RecentError(nil), r.recentErrors[len(r.recentErrors)-r.recentErrorsLimit:]...)
	}
}

func cloneCounter(in map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sanitizeList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := sanitizeMessage(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func sanitizeMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 240 {
		value = value[:240]
	}
	for _, marker := range []string{"api_key=", "Bearer ", "s2a_", "sk-", "rt-"} {
		if strings.Contains(value, marker) {
			return "[redacted]"
		}
	}
	return value
}
