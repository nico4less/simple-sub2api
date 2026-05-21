package routing_test

import (
	"testing"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/routing"
)

func TestDecideSupportsTaskAndModelRules(t *testing.T) {
	cfg := config.DefaultConfig().Routing
	cfg.Rules = append(cfg.Rules, config.RoutingRule{TaskType: "code", ModelPatterns: []string{"gpt-4*"}, PreferTiers: []string{"advanced"}, FallbackTiers: []string{"simple"}})
	decision := routing.Decide(cfg, routing.Request{TaskType: "code", Model: "gpt-4.1"})
	if len(decision.PreferTiers) != 1 || decision.PreferTiers[0] != "advanced" {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.TaskType != "code" || decision.MatchedRule == "default" {
		t.Fatalf("unexpected rule match: %#v", decision)
	}
}
