package routing

import (
	"path"
	"strings"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

type Request struct {
	TaskType string   `json:"task_type"`
	Model    string   `json:"model"`
	Tags     []string `json:"tags"`
}

type Decision struct {
	TaskType      string   `json:"task_type"`
	PreferTiers   []string `json:"prefer_tiers"`
	FallbackTiers []string `json:"fallback_tiers"`
	MatchedRule   string   `json:"matched_rule"`
}

func Decide(cfg config.RoutingConfig, req Request) Decision {
	taskType := strings.TrimSpace(req.TaskType)
	if taskType == "" {
		taskType = cfg.DefaultTaskType
	}
	if taskType == "" {
		taskType = "default"
	}
	for i, rule := range cfg.Rules {
		if ruleMatches(rule, taskType, req.Model, req.Tags) {
			prefer := firstNonEmpty(rule.PreferTiers, cfg.PreferTiers)
			fallback := firstNonEmpty(rule.FallbackTiers, cfg.FallbackTiers)
			return Decision{TaskType: taskType, PreferTiers: prefer, FallbackTiers: fallback, MatchedRule: rule.TaskType + "#" + strconvIndex(i)}
		}
	}
	return Decision{TaskType: taskType, PreferTiers: append([]string(nil), cfg.PreferTiers...), FallbackTiers: append([]string(nil), cfg.FallbackTiers...), MatchedRule: "default"}
}

func ruleMatches(rule config.RoutingRule, taskType string, model string, tags []string) bool {
	if rule.TaskType != "" && rule.TaskType != taskType {
		return false
	}
	if len(rule.ModelPatterns) > 0 && !matchesAnyPattern(rule.ModelPatterns, model) {
		return false
	}
	if len(rule.Tags) > 0 && !hasAnyTag(rule.Tags, tags) {
		return false
	}
	return true
}

func matchesAnyPattern(patterns []string, model string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matched, err := path.Match(strings.ToLower(pattern), strings.ToLower(model))
		if err == nil && matched {
			return true
		}
		if strings.EqualFold(pattern, model) {
			return true
		}
	}
	return false
}

func hasAnyTag(ruleTags []string, requestTags []string) bool {
	seen := map[string]bool{}
	for _, tag := range requestTags {
		seen[strings.ToLower(strings.TrimSpace(tag))] = true
	}
	for _, tag := range ruleTags {
		if seen[strings.ToLower(strings.TrimSpace(tag))] {
			return true
		}
	}
	return false
}

func firstNonEmpty(primary []string, fallback []string) []string {
	if len(primary) > 0 {
		return append([]string(nil), primary...)
	}
	return append([]string(nil), fallback...)
}

func strconvIndex(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(digits[i%10]) + out
		i /= 10
	}
	return out
}
