package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

func TestChatGPTAccountPlanFromPayloadPrefersOrgDefaultAndPaid(t *testing.T) {
	payload := map[string]any{
		"accounts": map[string]any{
			"acct-free": map[string]any{
				"account": map[string]any{"plan_type": "free", "is_default": true},
			},
			"acct-team": map[string]any{
				"account":     map[string]any{"plan_type": "team"},
				"entitlement": map[string]any{"expires_at": "2026-06-01T00:00:00Z"},
			},
		},
	}
	if got := chatGPTAccountPlanFromPayload(payload, "acct-team"); got.PlanType != "team" || got.SubscriptionExpiresAt != "2026-06-01T00:00:00Z" {
		t.Fatalf("org matched plan = %#v, want team with expiry", got)
	}
	if got := chatGPTAccountPlanFromPayload(payload, ""); got.PlanType != "free" {
		t.Fatalf("default plan = %#v, want free", got)
	}

	delete(payload["accounts"].(map[string]any)["acct-free"].(map[string]any)["account"].(map[string]any), "is_default")
	if got := chatGPTAccountPlanFromPayload(payload, "missing"); got.PlanType != "team" {
		t.Fatalf("paid fallback plan = %#v, want team", got)
	}
}

func TestNormalizeSubscriptionTierPreservesOpenAIPlanNames(t *testing.T) {
	cases := map[string]string{
		"free":       "free",
		"plus":       "plus",
		"pro":        "pro",
		"team":       "team",
		"enterprise": "enterprise",
		"ultra":      "ultra",
		"max":        "max",
	}
	for raw, want := range cases {
		if got := normalizeSubscriptionTier(raw); got != want {
			t.Fatalf("normalizeSubscriptionTier(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestApplyOpenAIOAuthTokenClaimsExtractsPlanAndOrg(t *testing.T) {
	claims := openAIOAuthTokenClaims{
		Email: "user@example.com",
		OpenAIAuth: &openAIOAuthClaimValues{
			ChatGPTAccountID: "acct-chatgpt",
			ChatGPTUserID:    "user-chatgpt",
			ChatGPTPlanType:  "plus",
			POID:             "org-poid",
			Organizations: []openAIOAuthOrganizationJWT{
				{ID: "org-free"},
				{ID: "org-plus", IsDefault: true},
			},
		},
	}
	credentials := map[string]string{}
	applyOpenAIOAuthTokenClaims(credentials, claims)
	if credentials["email"] != "user@example.com" || credentials["chatgpt_account_id"] != "acct-chatgpt" || credentials["chatgpt_user_id"] != "user-chatgpt" {
		t.Fatalf("identity claims not projected: %#v", credentials)
	}
	if credentials["plan_type"] != "plus" || credentials["subscription_tier"] != "plus" {
		t.Fatalf("plan claims not projected: %#v", credentials)
	}
	if credentials["organization_id"] != "org-plus" || credentials["poid"] != "org-poid" {
		t.Fatalf("organization claims not projected: %#v", credentials)
	}
}

func TestDecodeOpenAIOAuthTokenClaimsAcceptsJWTWithoutPadding(t *testing.T) {
	token := testJWT(t, map[string]any{
		"email": "team@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_plan_type": "team",
			"poid":              "org-team",
		},
	})
	claims, err := decodeOpenAIOAuthTokenClaims(token)
	if err != nil {
		t.Fatalf("decodeOpenAIOAuthTokenClaims() error = %v", err)
	}
	if claims.Email != "team@example.com" || claims.OpenAIAuth == nil || claims.OpenAIAuth.ChatGPTPlanType != "team" || claims.OpenAIAuth.POID != "org-team" {
		t.Fatalf("decoded claims = %#v", claims)
	}
}

func TestOpenAICodexUsageMetadataFromHeadersNormalizesFiveHourAndSevenDay(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "88")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "42")
	headers.Set("x-codex-secondary-reset-after-seconds", "18000")
	headers.Set("x-codex-secondary-window-minutes", "300")

	base := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	updates := openAICodexUsageMetadataFromHeaders(headers, base)
	if updates["codex_5h_used_percent"] != 42.0 || updates["codex_7d_used_percent"] != 88.0 {
		t.Fatalf("normalized codex usage updates = %#v", updates)
	}
	if updates["codex_5h_reset_at"] != "2026-05-25T15:00:00Z" || updates["codex_7d_reset_at"] != "2026-06-01T10:00:00Z" {
		t.Fatalf("normalized codex reset times = %#v", updates)
	}
}

func TestApplyOpenAIDisplayCredentialMetadataProjectsCredentialPlanAndCodexUsage(t *testing.T) {
	metadata := map[string]any{"platform": "openai"}
	applyOpenAIDisplayCredentialMetadata(metadata, "refresh_token=rt;access_token=at;subscription_tier=plus;plan_type=plus;codex_5h_used_percent=12;codex_5h_reset_after_seconds=600;codex_7d_used_percent=34;codex_7d_reset_after_seconds=86400")
	if metadata["subscription_tier"] != "plus" || metadata["plan_type"] != "plus" {
		t.Fatalf("plan metadata not projected: %#v", metadata)
	}
	usage := usageInfoFromCodexMetadata(metadata)
	if usage == nil {
		t.Fatalf("usage metadata not projected: %#v", metadata)
	}
	fiveHour, _ := usage["five_hour"].(map[string]any)
	sevenDay, _ := usage["seven_day"].(map[string]any)
	if fiveHour["utilization"] != 12.0 || sevenDay["utilization"] != 34.0 {
		t.Fatalf("usage windows = %#v", usage)
	}
}

func TestClaudeUsagePayloadProjectsSubscriptionAndWindows(t *testing.T) {
	payload := map[string]any{
		"subscription": map[string]any{"plan_type": "max"},
		"five_hour": map[string]any{
			"utilization": 12.5,
			"resets_at":   "2026-06-01T00:00:00Z",
		},
		"seven_day": map[string]any{
			"remaining_fraction": 0.66,
			"reset_time":         "2026-06-07T00:00:00Z",
		},
		"seven_day_sonnet": map[string]any{
			"used_percent":        56.0,
			"reset_after_seconds": 3600,
			"window_stats":        map[string]any{"cost": 1.25},
		},
		"user": map[string]any{"plan_type": "pro"},
	}
	usage := usageInfoFromClaudeUsagePayload(payload)
	if usage == nil {
		t.Fatalf("usage not projected")
	}
	fiveHour, _ := usage["five_hour"].(map[string]any)
	sevenDay, _ := usage["seven_day"].(map[string]any)
	sonnet, _ := usage["seven_day_sonnet"].(map[string]any)
	if fiveHour["utilization"] != 12.5 || sevenDay["utilization"] != 34.0 || sonnet["utilization"] != 56.0 {
		t.Fatalf("unexpected Claude usage windows: %#v", usage)
	}
	if got := claudeSubscriptionTierFromPayload(payload); got != "pro" {
		t.Fatalf("claude tier = %q, want pro", got)
	}
}

func TestClaudeUsagePayloadProjectsCredentialUserPlanBeforeFreeFallback(t *testing.T) {
	payload := map[string]any{
		"subscription": map[string]any{"plan_type": "free"},
		"organization": map[string]any{
			"subscription": map[string]any{"plan_type": "max"},
		},
	}
	if got := claudeSubscriptionTierFromPayload(payload); got != "max" {
		t.Fatalf("claude tier = %q, want max", got)
	}
}

func TestClaudeOAuthTokenBundleProjectsPlanFromAccountInfo(t *testing.T) {
	token := claudeOAuthTokenResponse{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresIn:    3600,
		Account: &claudeOAuthAccountInfo{
			UUID:         "acct-uuid",
			EmailAddress: "claude@example.test",
			Subscription: map[string]any{"plan_type": "pro"},
		},
	}
	credentials := claudeOAuthCredentialsFromToken(token, "client-id")
	if credentials["subscription_tier"] != "pro" || credentials["plan_type"] != "pro" {
		t.Fatalf("Claude token plan not projected: %#v", credentials)
	}
	merged := credentialsWithStoredAccountCredential(map[string]string{}, credentialEnvelope(credentials))
	if merged["subscription_tier"] != "pro" || merged["plan_type"] != "pro" {
		t.Fatalf("stored Claude token plan not preserved: %#v", merged)
	}
}

func TestApplyDisplayStateDoesNotFallbackClaudeOAuthRoutingTierToFree(t *testing.T) {
	summary := &accountSummary{}
	applyDisplayState(summary, config.Account{ID: "acct", Type: "oauth", Tier: "simple", Metadata: map[string]any{"platform": "anthropic"}})
	if summary.SubscriptionTier != "" {
		t.Fatalf("subscription tier = %q, want empty until real tier is known", summary.SubscriptionTier)
	}
}

func TestApplyClaudeDisplayCredentialMetadataProjectsCredentialUsage(t *testing.T) {
	metadata := map[string]any{"platform": "anthropic"}
	applyClaudeDisplayCredentialMetadata(metadata, "access_token=at;subscription_tier=max;claude_5h_used_percent=21;claude_5h_reset_after_seconds=600;claude_7d_used_percent=43;claude_7d_sonnet_used_percent=65")
	if metadata["subscription_tier"] != "max" {
		t.Fatalf("Claude tier metadata not projected: %#v", metadata)
	}
	usage := usageInfoFromClaudeMetadata(metadata)
	fiveHour, _ := usage["five_hour"].(map[string]any)
	sevenDay, _ := usage["seven_day"].(map[string]any)
	sonnet, _ := usage["seven_day_sonnet"].(map[string]any)
	if fiveHour["utilization"] != 21.0 || sevenDay["utilization"] != 43.0 || sonnet["utilization"] != 65.0 {
		t.Fatalf("Claude credential usage windows = %#v", usage)
	}
}

func credentialEnvelope(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, ";")
}

func testJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(body) + "."
}
