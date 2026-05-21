package subscriptionimport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/0xForce-Network/simple-sub2api/internal/accountcompat"
	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

func TestPreviewImportLineTokensRedactsCredentialsAndDoesNotMutateConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	preview, err := PreviewImport(context.Background(), Request{
		Kind:        "line_tokens",
		Content:     "sk-one\nsk-two\nsk-one\n",
		Label:       "Personal tokens",
		Tier:        "simple",
		Tags:        []string{"document"},
		Platform:    accountcompat.PlatformOpenAI,
		AccountType: accountcompat.AccountTypeAPIKey,
	}, cfg)
	if err != nil {
		t.Fatalf("PreviewImport() error = %v", err)
	}
	if len(preview.Accounts) != 3 {
		t.Fatalf("preview accounts = %d, want 3", len(preview.Accounts))
	}
	if preview.ImportResult.AccountCreated != 3 {
		t.Fatalf("import result = %#v", preview.ImportResult)
	}
	if strings.Contains(preview.Accounts[0].Credential, "sk-one") {
		t.Fatal("preview leaked full credential")
	}
	if len(cfg.Accounts) != 0 {
		t.Fatal("preview mutated existing config")
	}
}

func TestPrepareImportApplyPreservesCredentialsForSave(t *testing.T) {
	cfg := config.DefaultConfig()
	prepared, err := PrepareImport(context.Background(), Request{Kind: "line_tokens", Content: "sk-live-token", Tier: "simple", Platform: accountcompat.PlatformOpenAI, AccountType: accountcompat.AccountTypeAPIKey}, cfg, false)
	if err != nil {
		t.Fatalf("PrepareImport() error = %v", err)
	}
	if got := prepared.Accounts[0].Credential; !strings.Contains(got, "api_key=sk-live-token") {
		t.Fatalf("prepared credential = %q", got)
	}
	saved := cfg
	ApplyPreview(&saved, prepared)
	if err := config.EnsureDefaultsAndSecrets(&saved); err != nil {
		t.Fatalf("EnsureDefaultsAndSecrets() error = %v", err)
	}
	if err := config.Validate(saved); err != nil {
		t.Fatalf("Validate(saved) error = %v", err)
	}
}

func TestPreviewImportSub2APIDataPayload(t *testing.T) {
	proxyKey := accountcompat.BuildProxyKey("http", "127.0.0.1", 8081, "", "")
	payload := accountcompat.DataPayload{
		Type:       accountcompat.DataType,
		Version:    accountcompat.DataVersion,
		ExportedAt: "2026-05-18T00:00:00Z",
		Proxies:    []accountcompat.DataProxy{{ProxyKey: proxyKey, Name: "p", Protocol: "http", Host: "127.0.0.1", Port: 8081, Status: "active"}},
		Accounts: []accountcompat.DataAccount{{
			Name:        "Gemini OAuth",
			Platform:    accountcompat.PlatformGemini,
			Type:        accountcompat.AccountTypeOAuth,
			Credentials: map[string]any{"refresh_token": "gemini-rt"},
			ProxyKey:    &proxyKey,
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	preview, err := PreviewImport(context.Background(), Request{Kind: "json_bundle", Content: string(data), Tier: "advanced", Tags: []string{"code"}}, config.DefaultConfig())
	if err != nil {
		t.Fatalf("PreviewImport() error = %v", err)
	}
	if preview.AccountData == nil || preview.AccountData.Type != accountcompat.DataType {
		t.Fatalf("missing account data payload: %#v", preview.AccountData)
	}
	if preview.ImportResult.ProxyCreated != 1 || preview.ImportResult.AccountCreated != 1 {
		t.Fatalf("import result = %#v", preview.ImportResult)
	}
	if strings.Contains(preview.Accounts[0].Credential, "gemini-rt") {
		t.Fatal("preview leaked upstream refresh token")
	}
}

func TestImpactForSourceListsDerivedAccounts(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Accounts = []config.Account{
		{ID: "acct_a", SourceID: "source_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "sk-a", Enabled: true},
		{ID: "acct_b", SourceID: "source_1", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "sk-b", Enabled: true},
	}
	impact := ImpactForSource("source_1", cfg)
	if len(impact.DerivedAccounts) != 2 || impact.DerivedAccounts[0] != "acct_a" || impact.DerivedAccounts[1] != "acct_b" {
		t.Fatalf("impact = %#v", impact)
	}
}
