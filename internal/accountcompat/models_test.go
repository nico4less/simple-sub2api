package accountcompat

import (
	"strings"
	"testing"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

func TestValidatePayloadHeaderMatchesUpstreamContract(t *testing.T) {
	valid := DataPayload{Type: DataType, Version: DataVersion, Proxies: []DataProxy{}, Accounts: []DataAccount{}}
	if err := ValidatePayloadHeader(valid); err != nil {
		t.Fatalf("ValidatePayloadHeader() error = %v", err)
	}
	invalid := valid
	invalid.Type = "other"
	if err := ValidatePayloadHeader(invalid); err == nil {
		t.Fatal("ValidatePayloadHeader() accepted unsupported type")
	}
}

func TestValidateDataAccountMatchesUpstreamStaticRules(t *testing.T) {
	item := DataAccount{Name: "OpenAI OAuth", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"refresh_token": "rt"}}
	if err := ValidateDataAccount(item); err != nil {
		t.Fatalf("ValidateDataAccount() error = %v", err)
	}
	item.Type = "local-only-type"
	if err := ValidateDataAccount(item); err == nil {
		t.Fatal("ValidateDataAccount() accepted incompatible account type")
	}
}

func TestPreparePayloadPreservesSub2APIProxyAndCredentialEnvelope(t *testing.T) {
	proxyKey := BuildProxyKey("socks5", "127.0.0.1", 1080, "u", "p")
	payload := DataPayload{
		Type:       DataType,
		Version:    DataVersion,
		ExportedAt: "2026-05-18T00:00:00Z",
		Proxies:    []DataProxy{{ProxyKey: proxyKey, Name: "local", Protocol: "socks5", Host: "127.0.0.1", Port: 1080, Username: "u", Password: "p", Status: "active"}},
		Accounts:   []DataAccount{{Name: "Codex session", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"refresh_token": "rt-secret", "codex_session": "session-secret"}, ProxyKey: &proxyKey}},
	}
	prepared, err := PreparePayload(payload, config.DefaultConfig(), "compat", "advanced", []string{"code"}, false)
	if err != nil {
		t.Fatalf("PreparePayload() error = %v", err)
	}
	if prepared.Result.ProxyCreated != 1 || prepared.Result.AccountCreated != 1 {
		t.Fatalf("result = %#v", prepared.Result)
	}
	if got := prepared.Proxies[0].URL; !strings.HasPrefix(got, "socks5h://") {
		t.Fatalf("proxy URL = %q", got)
	}
	credential := prepared.Accounts[0].Credential
	if !strings.Contains(credential, "codex_session=session-secret") || !strings.Contains(credential, "refresh_token=rt-secret") {
		t.Fatalf("credential envelope = %q", credential)
	}
}

func TestPayloadFromLineTokensUsesUpstreamOAuthRefreshTokenEnvelope(t *testing.T) {
	payload := PayloadFromLineTokens("rt-one\n", PlatformGemini, AccountTypeOAuth)
	if len(payload.Accounts) != 1 {
		t.Fatalf("accounts = %d", len(payload.Accounts))
	}
	if got := payload.Accounts[0].Credentials["refresh_token"]; got != "rt-one" {
		t.Fatalf("refresh_token = %v", got)
	}
}
