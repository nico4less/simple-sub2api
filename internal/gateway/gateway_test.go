package gateway_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/dashboard"
	"github.com/0xForce-Network/simple-sub2api/internal/gateway"
	"github.com/0xForce-Network/simple-sub2api/internal/server"
)

func TestChatCompletionsNonStreamGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			if got := r.Header.Get("Authorization"); got != "Bearer sk-upstream" {
				t.Fatalf("upstream Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":17}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Simple-Sub2API-Account"); got != "acct_1" {
		t.Fatalf("selected account = %q", got)
	}
	quotaResp := doGatewayRequest(t, srv.URL+"/api/admin/quota/state", "", nil)
	_ = quotaResp.Body.Close()
}

func TestResponsesGatewayForCodexClient(t *testing.T) {
	var gotPath string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses":
			gotPath = r.URL.Path
			var err error
			gotBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read upstream body: %v", err)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer sk-upstream" {
				t.Fatalf("upstream Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","output_text":"ok","usage":{"total_tokens":11}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","input":"who are you"}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("upstream body = %q, want %q", string(gotBody), string(body))
	}
}

func TestResponsesGatewayNormalizesDuplicatedV1Prefix(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses":
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","output_text":"ok","usage":{"total_tokens":11}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	resp := doGatewayRequest(t, srv.URL+"/v1/v1/responses", store.GatewayKey(), []byte(`{"model":"gpt-test","input":"who are you"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("upstream path = %q, want %q", gotPath, "/v1/responses")
	}
}

func TestChatCompletionsGatewayNormalizesDuplicatedV1Prefix(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":17}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	resp := doGatewayRequest(t, srv.URL+"/v1/v1/chat/completions", store.GatewayKey(), []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want %q", gotPath, "/v1/chat/completions")
	}
}

func TestAnthropicMessagesGatewayForClaudeCLIApiKeyAccount(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotAuth string
	var gotAPIKey string
	var gotVersion string
	var gotBeta string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/messages":
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			gotAuth = r.Header.Get("Authorization")
			gotAPIKey = r.Header.Get("X-Api-Key")
			gotVersion = r.Header.Get("Anthropic-Version")
			gotBeta = r.Header.Get("Anthropic-Beta")
			var err error
			gotBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read upstream body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":7,"output_tokens":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "openai_compatible", Label: "Claude", Tier: "simple", Credential: "api_key=sk-ant-upstream", BaseURL: upstream.URL, Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}, "anthropic", config.GroupRotationPolicy{Strategy: "polling", RetryOnErrors: false})
	defer srv.Close()
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"who are you"}]}],"stream":false}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/v1/messages?beta=true", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219")
	req.Header.Set("User-Agent", "claude-cli/2.1.126 (external, cli)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if gotPath != "/v1/messages" || gotQuery != "beta=true" {
		t.Fatalf("upstream target = %q?%q, want /v1/messages?beta=true", gotPath, gotQuery)
	}
	if gotAuth != "" || gotAPIKey != "sk-ant-upstream" {
		t.Fatalf("upstream auth headers Authorization=%q X-Api-Key=%q", gotAuth, gotAPIKey)
	}
	if gotVersion != "2023-06-01" || gotBeta != "claude-code-20250219" {
		t.Fatalf("anthropic headers version=%q beta=%q", gotVersion, gotBeta)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("upstream body = %q, want %q", string(gotBody), string(body))
	}
}

func TestAnthropicMessagesGatewayForClaudeCLIOAuthAccount(t *testing.T) {
	var gotAuth string
	var gotAPIKey string
	var gotBeta string
	var gotQuery string
	var gotUserAgent string
	var gotXApp string
	var gotStainlessLang string
	var gotStainlessPackage string
	var gotClientRequestID string
	var gotSessionHeader string
	var gotAcceptEncoding string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/messages":
			gotAuth = r.Header.Get("Authorization")
			gotAPIKey = r.Header.Get("X-Api-Key")
			gotBeta = r.Header.Get("Anthropic-Beta")
			gotQuery = r.URL.RawQuery
			gotUserAgent = r.Header.Get("User-Agent")
			gotXApp = r.Header.Get("X-App")
			gotStainlessLang = r.Header.Get("X-Stainless-Lang")
			gotStainlessPackage = r.Header.Get("X-Stainless-Package-Version")
			gotClientRequestID = r.Header.Get("X-Client-Request-Id")
			gotSessionHeader = r.Header.Get("X-Claude-Code-Session-Id")
			gotAcceptEncoding = r.Header.Get("Accept-Encoding")
			var err error
			gotBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read upstream body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":7,"output_tokens":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "oauth", Label: "Claude", Tier: "simple", Credential: "access_token=claude-oauth-access;account_uuid=acct-upstream-uuid", BaseURL: upstream.URL, Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}, "anthropic", config.GroupRotationPolicy{Strategy: "polling", RetryOnErrors: false})
	defer srv.Close()
	bodyPayload := map[string]any{
		"model":      "claude-sonnet-4-6",
		"max_tokens": 64,
		"messages":   []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "who are you"}}}},
		"stream":     false,
		"metadata":   map[string]any{"user_id": `user_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_account_old-account_session_11111111-2222-3333-4444-555555555555`},
	}
	body, err := json.Marshal(bodyPayload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages?beta=true", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219")
	req.Header.Set("User-Agent", "roo-code/3.53.0")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	req.Header.Set("X-Stainless-Package-Version", "0.81.0")
	req.Header.Set("X-Claude-Code-Session-Id", "11111111-2222-3333-4444-555555555555")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if gotAuth != "Bearer claude-oauth-access" || gotAPIKey != "" {
		t.Fatalf("upstream auth headers Authorization=%q X-Api-Key=%q", gotAuth, gotAPIKey)
	}
	if gotQuery != "beta=true" {
		t.Fatalf("upstream query = %q, want beta=true", gotQuery)
	}
	if !strings.Contains(gotBeta, "claude-code-20250219") || !strings.Contains(gotBeta, "oauth-2025-04-20") {
		t.Fatalf("Anthropic-Beta = %q", gotBeta)
	}
	for _, token := range []string{"context-1m-2025-08-07", "interleaved-thinking-2025-05-14", "redact-thinking-2026-02-12", "prompt-caching-scope-2026-01-05", "effort-2025-11-24", "context-management-2025-06-27"} {
		if !strings.Contains(gotBeta, token) {
			t.Fatalf("Anthropic-Beta missing %q: %q", token, gotBeta)
		}
	}
	if gotUserAgent != "claude-cli/2.1.126 (external, cli)" || gotXApp != "cli" || gotStainlessLang != "js" {
		t.Fatalf("mimic headers User-Agent=%q X-App=%q X-Stainless-Lang=%q", gotUserAgent, gotXApp, gotStainlessLang)
	}
	if gotStainlessPackage != "0.81.0" {
		t.Fatalf("X-Stainless-Package-Version = %q", gotStainlessPackage)
	}
	if strings.Contains(gotAcceptEncoding, "br") || strings.Contains(gotAcceptEncoding, "zstd") || strings.Contains(gotAcceptEncoding, "deflate") {
		t.Fatalf("client Accept-Encoding leaked upstream: %q", gotAcceptEncoding)
	}
	if gotClientRequestID == "" {
		t.Fatal("X-Client-Request-Id was not set")
	}
	var upstreamPayload struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(gotBody, &upstreamPayload); err != nil {
		t.Fatalf("Unmarshal upstream body: %v body=%s", err, string(gotBody))
	}
	gotBodyText := string(gotBody)
	if !regexp.MustCompile(`x-anthropic-billing-header: cc_version=2\.1\.126\.df2; cc_entrypoint=cli; cch=[0-9a-f]{5};`).MatchString(gotBodyText) {
		t.Fatalf("mimic body missing signed Claude billing header: %s", gotBodyText)
	}
	if strings.Contains(gotBodyText, "cch=00000") {
		t.Fatalf("mimic body kept unsigned cch placeholder: %s", gotBodyText)
	}
	userID, _ := upstreamPayload.Metadata["user_id"].(string)
	if userID == "" || strings.Contains(userID, "old-account") || strings.Contains(userID, "0123456789abcdef") {
		t.Fatalf("metadata.user_id was not rewritten safely: %q", userID)
	}
	var parsedUID struct {
		DeviceID    string `json:"device_id"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(userID), &parsedUID); err != nil {
		t.Fatalf("metadata.user_id is not JSON new format: %q err=%v", userID, err)
	}
	if parsedUID.AccountUUID != "acct-upstream-uuid" || parsedUID.SessionID == "11111111-2222-3333-4444-555555555555" || parsedUID.SessionID == "" {
		t.Fatalf("rewritten metadata.user_id = %#v", parsedUID)
	}
	if gotSessionHeader != parsedUID.SessionID {
		t.Fatalf("X-Claude-Code-Session-Id = %q, want rewritten session %q", gotSessionHeader, parsedUID.SessionID)
	}
}

func TestAnthropicMessagesGatewayForRealClaudeCLIOAuthAccountPreservesClientFingerprintAndSignsCCH(t *testing.T) {
	var gotBeta string
	var gotUserAgent string
	var gotStainlessPackage string
	var gotStainlessArch string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/messages":
			gotBeta = r.Header.Get("Anthropic-Beta")
			gotUserAgent = r.Header.Get("User-Agent")
			gotStainlessPackage = r.Header.Get("X-Stainless-Package-Version")
			gotStainlessArch = r.Header.Get("X-Stainless-Arch")
			var err error
			gotBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read upstream body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":7,"output_tokens":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "oauth", Label: "Claude", Tier: "simple", Credential: "access_token=claude-oauth-access;account_uuid=acct-upstream-uuid", BaseURL: upstream.URL, Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}, "anthropic", config.GroupRotationPolicy{Strategy: "polling", RetryOnErrors: false})
	defer srv.Close()
	originalUID := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","account_uuid":"old-account","session_id":"11111111-2222-3333-4444-555555555555"}`
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"who are you"}]}],"stream":false,"metadata":{"user_id":` + strconv.Quote(originalUID) + `},"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.126.df2; cc_entrypoint=cli; cch=00000;"},{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages?beta=true", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14")
	req.Header.Set("User-Agent", "claude-cli/2.1.126 (external, cli)")
	req.Header.Set("X-App", "cli")
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Stainless-Package-Version", "0.81.0")
	req.Header.Set("X-Stainless-Arch", "x64")
	req.Header.Set("X-Claude-Code-Session-Id", "11111111-2222-3333-4444-555555555555")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if gotUserAgent != "claude-cli/2.1.126 (external, cli)" || gotStainlessPackage != "0.81.0" || gotStainlessArch != "x64" {
		t.Fatalf("real Claude CLI fingerprint was not preserved: ua=%q package=%q arch=%q", gotUserAgent, gotStainlessPackage, gotStainlessArch)
	}
	if gotBeta != "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14" {
		t.Fatalf("Anthropic-Beta = %q", gotBeta)
	}
	got := string(gotBody)
	if strings.Contains(got, "cc_version=2.1.92") || !strings.Contains(got, "cc_version=2.1.126.df2") {
		t.Fatalf("billing cc_version should follow real CLI UA while preserving suffix: %s", got)
	}
	if strings.Contains(got, "cch=00000") || !regexp.MustCompile(`cch=[0-9a-f]{5};`).MatchString(got) {
		t.Fatalf("billing cch was not signed: %s", got)
	}
}

func TestAnthropicMessagesGatewayRefreshesOAuthAccessToken(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/messages":
			requestCount++
			if got := r.Header.Get("Authorization"); got != "Bearer claude-refreshed-access" {
				t.Fatalf("upstream Authorization = %q", got)
			}
			if got := r.Header.Get("X-Api-Key"); got != "" {
				t.Fatalf("upstream X-Api-Key = %q", got)
			}
			if got := r.Header.Get("Anthropic-Beta"); !strings.Contains(got, "oauth-2025-04-20") {
				t.Fatalf("Anthropic-Beta = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":7,"output_tokens":3}}`))
		case "/oauth/token":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("User-Agent") != "axios/1.13.6" {
				t.Fatalf("unexpected refresh headers Content-Type=%q User-Agent=%q", r.Header.Get("Content-Type"), r.Header.Get("User-Agent"))
			}
			if payload["grant_type"] != "refresh_token" || payload["refresh_token"] != "claude-refresh-token" || payload["client_id"] == "" {
				t.Fatalf("unexpected refresh payload: %#v", payload)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"claude-refreshed-access","refresh_token":"claude-refresh-new","expires_in":3600,"token_type":"Bearer","scope":"user:inference"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	credential := "refresh_token=claude-refresh-token;token_url=" + upstream.URL + "/oauth/token"
	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "oauth", Label: "Claude", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}, "anthropic", config.GroupRotationPolicy{Strategy: "polling", RetryOnErrors: false})
	defer srv.Close()
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"who are you"}]}],"stream":false}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages?beta=true", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "context-1m-2025-08-07")
	req.Header.Set("User-Agent", "claude-cli/2.1.126 (external, cli)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if requestCount != 1 {
		t.Fatalf("upstream request count = %d, want 1", requestCount)
	}
	credentialAfterRefresh := store.Snapshot().Accounts[0].Credential
	if !strings.Contains(credentialAfterRefresh, "access_token=claude-refreshed-access") || !strings.Contains(credentialAfterRefresh, "refresh_token=claude-refresh-new") || !strings.Contains(credentialAfterRefresh, "token_type=Bearer") || !strings.Contains(credentialAfterRefresh, "scope=user:inference") {
		t.Fatalf("refreshed credential was not persisted: %q", credentialAfterRefresh)
	}
}

func TestClaudeCLIModelsDoubleV1ReturnsModels(t *testing.T) {
	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "oauth", Label: "Claude", Tier: "simple", Credential: "access_token=claude-oauth-access", BaseURL: "https://api.anthropic.com", Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}, "anthropic", config.GroupRotationPolicy{Strategy: "polling", RetryOnErrors: false})
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/v1/models?limit=1000", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Anthropic-Version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
}

func TestResponsesStreamingGatewayReencodesForDefaultClients(t *testing.T) {
	upstreamBody := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(upstreamBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	resp := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), []byte(`{"model":"gpt-test","input":"who are you","stream":true}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	body := string(payload)
	if strings.Contains(body, "event: response.output_text.delta") {
		t.Fatalf("default responses stream unexpectedly preserved raw event frames: %q", body)
	}
	if !strings.Contains(body, `data: {"type":"response.output_text.delta","delta":"ok"}`) {
		t.Fatalf("reencoded responses stream missing delta payload: %q", body)
	}
	if !strings.Contains(body, `data: {"type":"response.completed"}`) {
		t.Fatalf("reencoded responses stream missing completed payload: %q", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("reencoded responses stream missing done sentinel: %q", body)
	}
}

func TestResponsesStreamingGatewayPassthroughsForRooClient(t *testing.T) {
	upstreamBody := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_123\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses":
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("X-Upstream-Trace", "roo-stream")
			_, _ = w.Write([]byte(upstreamBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-test","input":"who are you","stream":true}`)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "roo-code/3.18.4")
	req.Header.Set("Originator", "roo-code")
	req.Header.Set("X-Stainless-Lang", "js")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("X-Upstream-Trace"); got != "roo-stream" {
		t.Fatalf("X-Upstream-Trace = %q", got)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	body := string(payload)
	if body != upstreamBody {
		t.Fatalf("passthrough body = %q, want %q", body, upstreamBody)
	}
	if strings.Contains(body, "data: [DONE]") {
		t.Fatalf("passthrough responses stream appended unexpected done sentinel: %q", body)
	}
}

func TestAnthropicMessagesStreamingGatewayPassthroughsRooInternalSSE(t *testing.T) {
	upstreamBody := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/messages":
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("X-Upstream-Trace", "anthropic-stream")
			_, _ = w.Write([]byte(upstreamBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	policy := config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{429, 401, 403, 404, 500}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}
	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "oauth", Label: "Claude", Tier: "advanced", Credential: "access_token=claude-oauth-access;account_uuid=acct-upstream-uuid", BaseURL: upstream.URL, Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}, "anthropic", policy)
	defer srv.Close()

	body := []byte(`{"model":"claude-opus-4-7","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"who are you"}]}],"stream":true,"metadata":{"user_id":"{\"device_id\":\"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\",\"account_uuid\":\"old-account\",\"session_id\":\"11111111-2222-3333-4444-555555555555\"}"},"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.126.df2; cc_entrypoint=cli; cch=00000;"}],"tools":[],"output_config":{"effort":"high"},"thinking":{"type":"enabled"},"context_management":{"edits":[{"type":"clear_tool_uses_20250919","keep":"none"}]}}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages?beta=true", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.126 (external, cli)")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("X-App", "cli")
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Claude-Code-Session-Id", "6f098428-c130-40b7-ab0a-30e95a3b7bb1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("X-Upstream-Trace"); got != "anthropic-stream" {
		t.Fatalf("X-Upstream-Trace = %q", got)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	gotBody := string(payload)
	if gotBody != upstreamBody {
		t.Fatalf("passthrough body = %q, want %q", gotBody, upstreamBody)
	}
	if strings.Contains(gotBody, "data: [DONE]") {
		t.Fatalf("passthrough anthropic stream appended unexpected done sentinel: %q", gotBody)
	}
}

func TestChatCompletionsDoesNotLeakLocalMetadataUpstream(t *testing.T) {
	seen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			seen <- r.Header.Clone()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/NodeJS 4.5.6")
	req.Header.Set("OpenAI-Beta", "assistants=v2")
	req.Header.Set("X-Simple-Sub2API-Debug", "local-only")
	req.Header.Set("X-Simple-Task-Type", "document")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	upstreamHeaders := <-seen
	if got := upstreamHeaders.Get("Authorization"); got != "Bearer sk-upstream" {
		t.Fatalf("upstream Authorization = %q", got)
	}
	if got := upstreamHeaders.Get("User-Agent"); got != "OpenAI/NodeJS 4.5.6" {
		t.Fatalf("upstream User-Agent = %q", got)
	}
	if got := upstreamHeaders.Get("OpenAI-Beta"); got != "assistants=v2" {
		t.Fatalf("upstream OpenAI-Beta = %q", got)
	}
	if got := upstreamHeaders.Get("X-Simple-Sub2API-Debug"); got != "" {
		t.Fatalf("local debug header leaked upstream: %q", got)
	}
	if got := upstreamHeaders.Get("X-Simple-Task-Type"); got != "" {
		t.Fatalf("local routing header leaked upstream: %q", got)
	}
}

func TestChatCompletionsNormalizesOriginAndV1BaseURL(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
	}{
		{name: "origin", basePath: ""},
		{name: "v1 base", basePath: "/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models":
					_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
				case "/v1/chat/completions":
					gotPath = r.URL.Path
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":1}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()

			srv, store := gatewayServer(t, upstream.URL+tt.basePath)
			defer srv.Close()
			gotPath = ""
			body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
			resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if gotPath != "/v1/chat/completions" {
				t.Fatalf("upstream path = %q, want /v1/chat/completions", gotPath)
			}
		})
	}
}

func TestChatCompletionsStreamingGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"chunk-1\"}\n\ndata: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	foundPayload := false
	foundDone := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "chunk-1") {
			foundPayload = true
		}
		if line == "data: [DONE]" {
			foundDone = true
		}
	}
	if !foundPayload || !foundDone {
		t.Fatalf("stream payload found=%v done=%v", foundPayload, foundDone)
	}
}

func TestGatewayUpstreamErrorTriggersCooldown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("first status = %d", resp.StatusCode)
	}
	second := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second status after cooldown = %d", second.StatusCode)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(second.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode second error: %v", err)
	}
	if envelope.Error.Code != "group_exhausted" {
		t.Fatalf("error envelope = %#v", envelope)
	}
}

func TestAnthropicOAuthMessagesRateLimitDoesNotCooldownOrExhaustSingleAccount(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/messages":
			requestCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	policy := config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429, 401, 403, 404, 500}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}
	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "oauth", Label: "Claude", Tier: "advanced", Credential: "access_token=claude-oauth-access", BaseURL: upstream.URL, Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}, "anthropic", policy)
	defer srv.Close()

	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"who are you"}]}],"stream":false}`)
	first := doGatewayRequest(t, srv.URL+"/v1/messages", store.GatewayKey(), body)
	_ = first.Body.Close()
	if first.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("first status = %d, want %d", first.StatusCode, http.StatusTooManyRequests)
	}
	second := doGatewayRequest(t, srv.URL+"/v1/messages", store.GatewayKey(), body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		payload, _ := io.ReadAll(second.Body)
		t.Fatalf("second status = %d body=%s, want %d", second.StatusCode, string(payload), http.StatusTooManyRequests)
	}
	if requestCount != 2 {
		t.Fatalf("upstream request count = %d, want 2; account was likely cooldowned", requestCount)
	}
}

func TestOAuthLikeRateLimitDoesNotCooldownOrExhaustSingleAccount(t *testing.T) {
	for _, tt := range []struct {
		name     string
		platform string
	}{
		{name: "gemini", platform: "gemini"},
		{name: "antigravity", platform: "antigravity"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requestCount := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models":
					_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
				case "/v1/chat/completions":
					requestCount++
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()

			policy := config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429, 401, 403, 404, 500}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}
			srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_" + tt.platform, Type: "oauth", Label: tt.platform, Tier: "advanced", Credential: "access_token=oauth-access", BaseURL: upstream.URL, Metadata: map[string]any{"platform": tt.platform}, Enabled: true}}, tt.platform, policy)
			defer srv.Close()

			body := []byte(`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`)
			first := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
			_ = first.Body.Close()
			if first.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("first status = %d, want %d", first.StatusCode, http.StatusTooManyRequests)
			}
			second := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
			defer second.Body.Close()
			if second.StatusCode != http.StatusTooManyRequests {
				payload, _ := io.ReadAll(second.Body)
				t.Fatalf("second status = %d body=%s, want %d", second.StatusCode, string(payload), http.StatusTooManyRequests)
			}
			if requestCount != 2 {
				t.Fatalf("upstream request count = %d, want 2; account was likely cooldowned", requestCount)
			}
		})
	}
}

func TestResponsesTransientTLSFailureDoesNotCooldownAccount(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses":
			requestCount++
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer is not hijackable")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("Hijack() error = %v", err)
			}
			_, _ = conn.Write([]byte("\x16\x03\x03\x00\x01\x00"))
			_ = conn.Close()
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","input":"who are you","stream":true}`)
	first := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), body)
	_ = first.Body.Close()
	if first.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want %d", first.StatusCode, http.StatusServiceUnavailable)
	}
	second := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), body)
	_ = second.Body.Close()
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want %d", second.StatusCode, http.StatusServiceUnavailable)
	}
	if requestCount != 4 {
		t.Fatalf("upstream request count = %d, want 4; account was likely cooldowned or not internally retried", requestCount)
	}
}

func TestAnthropicMessagesTransientNetworkFailureRetriesAndDoesNotCooldown(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/messages":
			requestCount++
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer is not hijackable")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("Hijack() error = %v", err)
			}
			_, _ = conn.Write([]byte("not-http"))
			_ = conn.Close()
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	policy := config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429, 401, 403, 404, 500}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}
	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "oauth", Label: "Claude", Tier: "advanced", Credential: "access_token=claude-oauth-access", BaseURL: upstream.URL, Metadata: map[string]any{"platform": "anthropic"}, Enabled: true}}, "anthropic", policy)
	defer srv.Close()

	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"who are you"}]}],"stream":false}`)
	first := doGatewayRequest(t, srv.URL+"/v1/messages", store.GatewayKey(), body)
	_ = first.Body.Close()
	if first.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want %d", first.StatusCode, http.StatusServiceUnavailable)
	}
	if requestCount != 2 {
		t.Fatalf("request count after first gateway request = %d, want 2 internal retries", requestCount)
	}
	second := doGatewayRequest(t, srv.URL+"/v1/messages", store.GatewayKey(), body)
	_ = second.Body.Close()
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want %d", second.StatusCode, http.StatusServiceUnavailable)
	}
	if requestCount != 4 {
		t.Fatalf("request count after second gateway request = %d, want 4; account was likely cooldowned", requestCount)
	}
}

func TestGatewayForwardTimeoutUsesGatewayTimeoutNotProbeTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			time.Sleep(80 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Probe.TimeoutSeconds = 1
	cfg.Gateway.TimeoutSeconds = 2
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-upstream", BaseURL: upstream.URL, Enabled: true, QuotaPolicy: "daily", Tags: []string{"openai"}}}
	cfg.Quota.Policies = []config.QuotaPolicy{{ID: "daily", DailyLimitTokens: 1000, Source: "local"}}
	cfg.Groups = []config.Group{{ID: "openai", Name: "OpenAI", Platform: "openai", Status: "active", AccountIDs: []string{"acct_1"}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z", RotationPolicy: config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{429, 401, 403, 404, 500}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}}}
	cfg.GatewayKeys = []config.GatewayKey{{ID: "default", Name: "Default", KeyHash: config.HashGatewayKey("s2a_test_gateway_key_value"), Preview: config.KeyPreview("s2a_test_gateway_key_value"), Status: "enabled", RoutingPolicy: config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z"}}
	cfg.GatewayAuth.GatewayKey = "s2a_test_gateway_key_value"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestGatewayFailoverRotatesWithinGroupOnConfiguredStatus(t *testing.T) {
	seen := []string{}
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		seen = append(seen, "a")
		http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
	}))
	defer upstreamA.Close()
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		seen = append(seen, "b")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer upstreamB.Close()

	srv, store := gatewayServerWithAccounts(t, []config.Account{
		{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-a", BaseURL: upstreamA.URL, Enabled: true},
		{ID: "acct_2", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "api_key=sk-b", BaseURL: upstreamB.URL, Enabled: true},
	}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Simple-Sub2API-Account"); got != "acct_2" {
		t.Fatalf("selected account = %q, want acct_2", got)
	}
	if got := resp.Header.Get("X-Simple-Sub2API-Attempts"); got != "2" {
		t.Fatalf("attempts = %q, want 2", got)
	}
	if strings.Join(seen, ",") != "a,b" {
		t.Fatalf("upstream call order = %#v", seen)
	}
}

func TestGatewayFailoverRotatesWithinGroupWhenDefaultPolicyOmitted(t *testing.T) {
	seen := []string{}
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		seen = append(seen, "a")
		http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
	}))
	defer upstreamA.Close()
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		seen = append(seen, "b")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer upstreamB.Close()

	srv, store := gatewayServerWithAccounts(t, []config.Account{
		{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-a", BaseURL: upstreamA.URL, Enabled: true},
		{ID: "acct_2", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "api_key=sk-b", BaseURL: upstreamB.URL, Enabled: true},
	}, config.GroupRotationPolicy{})
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if got := resp.Header.Get("X-Simple-Sub2API-Account"); got != "acct_2" {
		t.Fatalf("selected account = %q, want acct_2", got)
	}
	if got := resp.Header.Get("X-Simple-Sub2API-Attempts"); got != "2" {
		t.Fatalf("attempts = %q, want 2", got)
	}
	if strings.Join(seen, ",") != "a,b" {
		t.Fatalf("upstream call order = %#v", seen)
	}
}

func TestGatewayStickySessionReusesSelectedGroupAccount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer upstream.Close()
	srv, store := gatewayServerWithAccounts(t, []config.Account{
		{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-a", BaseURL: upstream.URL, Enabled: true},
		{ID: "acct_2", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "api_key=sk-b", BaseURL: upstream.URL, Enabled: true},
	}, config.GroupRotationPolicy{Strategy: "polling", StickySessionsEnabled: true, StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	first := doGatewayRequestWithSession(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body, "sticky-a")
	_ = first.Body.Close()
	second := doGatewayRequestWithSession(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body, "sticky-a")
	defer second.Body.Close()
	if first.Header.Get("X-Simple-Sub2API-Account") == "" || first.Header.Get("X-Simple-Sub2API-Account") != second.Header.Get("X-Simple-Sub2API-Account") {
		t.Fatalf("sticky accounts first=%q second=%q", first.Header.Get("X-Simple-Sub2API-Account"), second.Header.Get("X-Simple-Sub2API-Account"))
	}
}

func TestGatewayGroupRoutingDoesNotRequireAccountTags(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer upstream.Close()
	srv, store := gatewayServerWithOptions(t, []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-a", BaseURL: upstream.URL, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}, false)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Simple-Sub2API-Account"); got != "acct_1" {
		t.Fatalf("selected account = %q", got)
	}
}

func TestGatewayActiveConnectionCoversSlowBodyLifecycle(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		once.Do(func() { close(firstStarted) })
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-releaseFirst
		_, _ = w.Write([]byte(`{"id":"slow","choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer upstreamA.Close()
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fast","choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer upstreamB.Close()
	srv, store := gatewayServerWithAccounts(t, []config.Account{
		{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-a", BaseURL: upstreamA.URL, Enabled: true},
		{ID: "acct_2", Type: "openai_api_key", Label: "B", Tier: "simple", Credential: "api_key=sk-b", BaseURL: upstreamB.URL, Enabled: true},
	}, config.GroupRotationPolicy{Strategy: "least_connections", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{429}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	firstDone := make(chan *http.Response, 1)
	go func() {
		firstDone <- doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	}()
	<-firstStarted
	second := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	defer second.Body.Close()
	if got := second.Header.Get("X-Simple-Sub2API-Account"); got != "acct_2" {
		close(releaseFirst)
		t.Fatalf("second selected account = %q, want acct_2 while acct_1 body is active", got)
	}
	close(releaseFirst)
	first := <-firstDone
	defer first.Body.Close()
}

func TestGatewayCooldownParsesRateLimitResetAndBodyCountdown(t *testing.T) {
	if duration := gateway.TestRetryAfterDuration(http.Header{"X-Ratelimit-Reset": []string{"120"}}); duration < 119*time.Second || duration > 121*time.Second {
		t.Fatalf("X-RateLimit-Reset duration = %v", duration)
	}
	_, duration := gateway.TestReadCooldownBody(strings.NewReader(`{"error":"Please try again in 5h23m"}`))
	if duration != 5*time.Hour+23*time.Minute {
		t.Fatalf("body cooldown duration = %v", duration)
	}
}

func TestGatewayThreeStrikeDisablesPermanentCredentialFailure(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		requestCount++
		http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
	}))
	defer upstream.Close()
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-a", BaseURL: upstream.URL, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	for i := 0; i < 3; i++ {
		resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
		_ = resp.Body.Close()
	}
	if requestCount != 3 {
		t.Fatalf("upstream request count = %d, want 3", requestCount)
	}
	if store.Snapshot().Accounts[0].Enabled {
		t.Fatal("account remained enabled after three permanent credential failures")
	}
	fourth := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	defer fourth.Body.Close()
	if fourth.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("fourth status after runtime disable = %d, want %d", fourth.StatusCode, http.StatusServiceUnavailable)
	}
	if requestCount != 3 {
		t.Fatalf("upstream request count after fourth request = %d, want 3", requestCount)
	}
}

func TestOAuthRefreshRequiresRetryAndReenablesDisabledRuntimeAccount(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses":
			requestCount++
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
				t.Fatalf("upstream Authorization = %q", got)
			}
		case "/backend-api/codex/responses":
			requestCount++
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
				t.Fatalf("upstream Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","output_text":"ok","usage":{"total_tokens":1}}`))
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt-test" {
				t.Fatalf("unexpected refresh form: %#v", r.Form)
			}
			if got := r.Form.Get("scope"); got != "" {
				t.Fatalf("refresh scope should be omitted to preserve granted API scopes, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-token","refresh_token":"rt-new","expires_in":3600,"token_type":"Bearer"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: "refresh_token=rt-test;token_url=" + upstream.URL + "/oauth/token", BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()

	resp := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), []byte(`{"model":"gpt-test","input":"who are you"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(body))
	}
	if requestCount != 1 {
		t.Fatalf("upstream request count = %d, want 1", requestCount)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Accounts) != 1 || !snapshot.Accounts[0].Enabled || !strings.Contains(snapshot.Accounts[0].Credential, "access_token=refreshed-token") || !strings.Contains(snapshot.Accounts[0].Credential, "refresh_token=rt-new") {
		t.Fatalf("refreshed account was not persisted/enabled: %#v", snapshot.Accounts)
	}
}

func TestOpenAIOAuthResponsesUsesCodexInternalEndpointAndHeaders(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/backend-api/codex/responses":
			requestCount++
			if got := r.Host; got != "chatgpt.com" {
				t.Fatalf("upstream Host = %q", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer oauth-access" {
				t.Fatalf("upstream Authorization = %q", got)
			}
			if got := r.Header.Get("chatgpt-account-id"); got != "chatgpt-acc" {
				t.Fatalf("chatgpt-account-id = %q", got)
			}
			if got := r.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
				t.Fatalf("OpenAI-Beta = %q", got)
			}
			if got := r.Header.Get("originator"); got != "codex_cli_rs" {
				t.Fatalf("originator = %q", got)
			}
			if got := r.Header.Get("User-Agent"); got != "codex_cli_rs/0.125.0" {
				t.Fatalf("User-Agent = %q", got)
			}
			if got := r.Header.Get("session_id"); got == "" || got == "client-session" {
				t.Fatalf("session_id was not isolated: %q", got)
			}
			if got := r.Header.Get("conversation_id"); got == "" || got == "client-conversation" {
				t.Fatalf("conversation_id was not isolated: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","output_text":"ok","usage":{"total_tokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	credential := "access_token=oauth-access;refresh_token=rt-test;expires_at=2099-01-01T00:00:00Z;chatgpt_account_id=chatgpt-acc"
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","input":"who are you","prompt_cache_key":"cache-key"}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenAI/Python 1.2.3")
	req.Header.Set("session_id", "client-session")
	req.Header.Set("conversation_id", "client-conversation")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if requestCount != 1 {
		t.Fatalf("codex upstream request count = %d, want 1", requestCount)
	}
}

func TestOpenAIOAuthResponsesGenericCodexPathUsesEventStreamAcceptAndReencodes(t *testing.T) {
	requestCount := 0
	upstreamBody := "event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/backend-api/codex/responses":
			requestCount++
			if got := r.Header.Get("Accept"); got != "text/event-stream" {
				t.Fatalf("Accept = %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if strings.Contains(string(body), `"max_output_tokens"`) {
				t.Fatalf("upstream body still contains unsupported max_output_tokens: %s", string(body))
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = w.Write([]byte(upstreamBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	credential := "access_token=oauth-access;refresh_token=rt-test;expires_at=2099-01-01T00:00:00Z"
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-test","input":"who are you","stream":true,"max_output_tokens":4096}`)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "OpenAI/Python 1.2.3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	body := string(payload)
	if strings.Contains(body, "event: response.created") {
		t.Fatalf("codex path should reencode instead of preserving raw events: %q", body)
	}
	if !strings.Contains(body, `data: {"type":"response.created"}`) {
		t.Fatalf("reencoded body missing response.created payload: %q", body)
	}
	if !strings.Contains(body, `data: {"type":"response.completed"}`) {
		t.Fatalf("reencoded body missing response.completed payload: %q", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("reencoded body missing done sentinel: %q", body)
	}
	if requestCount != 1 {
		t.Fatalf("codex upstream request count = %d, want 1", requestCount)
	}
}

func TestOpenAIOAuthResponsesCodexTUIUsesRawResponsesStreamPassthrough(t *testing.T) {
	requestCount := 0
	upstreamBody := "event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/backend-api/codex/responses":
			requestCount++
			if got := r.Header.Get("Accept"); got != "text/event-stream" {
				t.Fatalf("Accept = %q", got)
			}
			if got := r.Header.Get("User-Agent"); !strings.Contains(got, "codex-tui") {
				t.Fatalf("User-Agent = %q", got)
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = w.Write([]byte(upstreamBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	credential := "access_token=oauth-access;refresh_token=rt-test;expires_at=2099-01-01T00:00:00Z"
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-test","input":"who are you","stream":true}`)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "codex-tui/0.133.0")
	req.Header.Set("Originator", "codex-tui")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(payload) != upstreamBody {
		t.Fatalf("codex tui passthrough body = %q, want %q", string(payload), upstreamBody)
	}
	if requestCount != 1 {
		t.Fatalf("codex upstream request count = %d, want 1", requestCount)
	}
}

func TestOpenAIOAuthResponsesRooUsesCodexInternalEndpoint(t *testing.T) {
	officialRequestCount := 0
	codexRequestCount := 0
	upstreamBody := "event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses":
			officialRequestCount++
			http.Error(w, "should not hit official responses endpoint for roo oauth", http.StatusBadRequest)
		case "/backend-api/codex/responses":
			codexRequestCount++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if strings.Contains(string(body), `"max_output_tokens"`) {
				t.Fatalf("codex internal body still contains unsupported max_output_tokens: %s", string(body))
			}
			if got := r.Header.Get("Accept"); got != "text/event-stream" {
				t.Fatalf("Accept = %q", got)
			}
			if got := r.Header.Get("Originator"); got != "codex_cli_rs" {
				t.Fatalf("Originator = %q", got)
			}
			if got := r.Header.Get("User-Agent"); got != "codex_cli_rs/0.125.0" {
				t.Fatalf("User-Agent = %q", got)
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = w.Write([]byte(upstreamBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	credential := "access_token=oauth-access;refresh_token=rt-test;expires_at=2099-01-01T00:00:00Z"
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-test","input":"who are you","stream":true,"max_output_tokens":4096}`)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+store.GatewayKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "roo-code/3.53.0")
	req.Header.Set("Originator", "roo-code")
	req.Header.Set("X-Stainless-Lang", "js")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(payload) != upstreamBody {
		t.Fatalf("passthrough body = %q, want %q", string(payload), upstreamBody)
	}
	if officialRequestCount != 0 {
		t.Fatalf("official upstream request count = %d, want 0", officialRequestCount)
	}
	if codexRequestCount != 1 {
		t.Fatalf("codex upstream request count = %d, want 1", codexRequestCount)
	}
}

func TestOpenAIOAuthResponsesUnauthorizedDoesNotAutoDisableOrCooldownAccount(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/codex/responses":
			requestCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"chatgpt session rejected"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	credential := "access_token=oauth-access;refresh_token=rt-test;expires_at=2099-01-01T00:00:00Z"
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","input":"who are you"}`)
	for i := 0; i < 3; i++ {
		resp := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("responses request %d status = %d", i+1, resp.StatusCode)
		}
	}
	if requestCount != 3 {
		t.Fatalf("codex request count = %d, want 3", requestCount)
	}
	if !store.Snapshot().Accounts[0].Enabled {
		t.Fatal("openai oauth account was auto-disabled by repeated codex 401 responses")
	}
}

func TestOpenAIOAuthResponsesRefreshesAfterUnauthorizedWithoutCooldown(t *testing.T) {
	codexRequestCount := 0
	tokenRequestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/codex/responses":
			codexRequestCount++
			switch r.Header.Get("Authorization") {
			case "Bearer stale-token":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"expired chatgpt session"}}`))
			case "Bearer refreshed-token":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","output_text":"ok","usage":{"total_tokens":1}}`))
			default:
				t.Fatalf("upstream Authorization = %q", r.Header.Get("Authorization"))
			}
		case "/oauth/token":
			tokenRequestCount++
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if r.Form.Get("refresh_token") != "rt-test" {
				t.Fatalf("refresh_token = %q", r.Form.Get("refresh_token"))
			}
			if got := r.Form.Get("scope"); got != "" {
				t.Fatalf("refresh scope should be omitted to preserve granted API scopes, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"refreshed-token","refresh_token":"rt-new","expires_in":3600,"token_type":"Bearer"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	credential := "access_token=stale-token;refresh_token=rt-test;expires_at=2099-01-01T00:00:00Z;token_url=" + upstream.URL + "/oauth/token"
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: true, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()

	body := []byte(`{"model":"gpt-test","input":"who are you"}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first responses status = %d", resp.StatusCode)
	}
	if codexRequestCount != 2 || tokenRequestCount != 1 {
		t.Fatalf("after first request codex=%d token=%d, want 2/1", codexRequestCount, tokenRequestCount)
	}
	credentialAfterRefresh := store.Snapshot().Accounts[0].Credential
	if !strings.Contains(credentialAfterRefresh, "access_token=refreshed-token") || !strings.Contains(credentialAfterRefresh, "refresh_token=rt-new") {
		t.Fatalf("refreshed credential was not persisted: %q", credentialAfterRefresh)
	}

	resp = doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second responses status = %d", resp.StatusCode)
	}
	if codexRequestCount != 3 || tokenRequestCount != 1 {
		t.Fatalf("after second request codex=%d token=%d, want 3/1", codexRequestCount, tokenRequestCount)
	}
}

func TestOpenAIOAuthResponsesPersistsCodexQuotaSnapshot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-codex-primary-used-percent", "34")
		w.Header().Set("x-codex-primary-reset-after-seconds", "86400")
		w.Header().Set("x-codex-primary-window-minutes", "10080")
		w.Header().Set("x-codex-secondary-used-percent", "12")
		w.Header().Set("x-codex-secondary-reset-after-seconds", "600")
		w.Header().Set("x-codex-secondary-window-minutes", "300")
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","usage":{"total_tokens":3}}`))
	}))
	defer upstream.Close()

	credential := "access_token=oauth-access;refresh_token=rt-test;expires_at=2099-01-01T00:00:00Z"
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()

	resp := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), []byte(`{"model":"gpt-test","input":"hi"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("responses status = %d body=%s", resp.StatusCode, string(payload))
	}

	metadata := store.Snapshot().Accounts[0].Metadata
	if got := metadata["codex_5h_used_percent"]; got != 12.0 {
		t.Fatalf("codex_5h_used_percent = %#v, want 12", got)
	}
	if got := metadata["codex_7d_used_percent"]; got != 34.0 {
		t.Fatalf("codex_7d_used_percent = %#v, want 34", got)
	}
	if got := metadata["codex_5h_reset_after_seconds"]; got != 600 {
		t.Fatalf("codex_5h_reset_after_seconds = %#v, want 600", got)
	}
	if got := metadata["codex_7d_reset_after_seconds"]; got != 86400 {
		t.Fatalf("codex_7d_reset_after_seconds = %#v, want 86400", got)
	}
	if got, ok := metadata["codex_usage_updated_at"].(string); !ok || got == "" {
		t.Fatalf("codex_usage_updated_at = %#v, want non-empty string", metadata["codex_usage_updated_at"])
	}
}

func TestDisabledOpenAIOAuthWithValidAccessTokenIsPersistentlyReenabled(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/codex/responses":
			requestCount++
			if got := r.Header.Get("Authorization"); got != "Bearer oauth-access" {
				t.Fatalf("upstream Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","output_text":"ok","usage":{"total_tokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	credential := "access_token=oauth-access;refresh_token=rt-test;expires_at=2099-01-01T00:00:00Z"
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: credential, BaseURL: upstream.URL, Metadata: map[string]any{"platform": "openai"}, Enabled: false}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()
	resp := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), []byte(`{"model":"gpt-test","input":"who are you"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(payload))
	}
	if requestCount != 1 {
		t.Fatalf("codex request count = %d, want 1", requestCount)
	}
	if !store.Snapshot().Accounts[0].Enabled {
		t.Fatal("disabled oauth account with valid access token was not persistently re-enabled")
	}
}

func TestResponsesNotFoundDoesNotAutoDisableAccount(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses":
			requestCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream responses endpoint unavailable"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	srv, store := gatewayServerWithAccounts(t, []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-a", BaseURL: upstream.URL, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{401}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","input":"who are you"}`)
	for i := 0; i < 3; i++ {
		resp := doGatewayRequest(t, srv.URL+"/v1/responses", store.GatewayKey(), body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("responses request %d status = %d", i+1, resp.StatusCode)
		}
	}
	if requestCount != 3 {
		t.Fatalf("responses request count = %d", requestCount)
	}
	if !store.Snapshot().Accounts[0].Enabled {
		t.Fatal("account was auto-disabled by repeated /v1/responses 404 responses")
	}
}

func TestQueueFGatewayRecordsMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":9}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"metadata":{"task_type":"document"}}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status = %d", resp.StatusCode)
	}
	cookie := loginCookie(t, srv.URL, "admin-secret")
	metricsReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/metrics", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	metricsReq.AddCookie(cookie)
	metricsResp, err := http.DefaultClient.Do(metricsReq)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer metricsResp.Body.Close()
	var snapshot struct {
		TotalRequests   uint64            `json:"total_requests"`
		SuccessRequests uint64            `json:"success_requests"`
		PerAccountHits  map[string]uint64 `json:"per_account_hits"`
		TopUsage        []struct {
			AccountID    string `json:"account_id"`
			GatewayKeyID string `json:"gateway_key_id"`
			Requests     uint64 `json:"requests"`
			Successes    uint64 `json:"successes"`
			Errors       uint64 `json:"errors"`
		} `json:"top_usage"`
		RoutingDecisions []struct {
			SelectedAccount string `json:"selected_account"`
		} `json:"routing_decisions"`
	}
	if err := json.NewDecoder(metricsResp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if snapshot.TotalRequests != 1 || snapshot.SuccessRequests != 1 || snapshot.PerAccountHits["acct_1"] != 1 {
		t.Fatalf("metrics counters = %#v", snapshot)
	}
	if len(snapshot.RoutingDecisions) != 1 || snapshot.RoutingDecisions[0].SelectedAccount != "acct_1" {
		t.Fatalf("routing metrics = %#v", snapshot.RoutingDecisions)
	}
	if len(snapshot.TopUsage) != 1 || snapshot.TopUsage[0].AccountID != "acct_1" || snapshot.TopUsage[0].GatewayKeyID != "default" || snapshot.TopUsage[0].Requests != 1 || snapshot.TopUsage[0].Successes != 1 || snapshot.TopUsage[0].Errors != 0 {
		t.Fatalf("top usage metrics = %#v", snapshot.TopUsage)
	}
}

func TestQueueFGatewayRecordsAnthropicMessagesMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/messages":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":7,"output_tokens":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	policy := config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{429, 401, 403, 404, 500}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1}
	srv, store := gatewayServerWithAccountsAndPlatform(t, []config.Account{{ID: "acct_claude", Type: "oauth", Label: "Claude A", Tier: "advanced", Credential: "api_key=sk-ant-test", BaseURL: upstream.URL, QuotaPolicy: "", Metadata: map[string]any{"platform": "anthropic", "account_uuid": "acct-uuid-test"}, Enabled: true}}, "anthropic", policy)
	defer srv.Close()

	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"who are you"}]}],"stream":false}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/messages?beta=true", store.GatewayKey(), body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status = %d", resp.StatusCode)
	}

	cookie := loginCookie(t, srv.URL, "admin-secret")
	metricsReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/metrics", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	metricsReq.AddCookie(cookie)
	metricsResp, err := http.DefaultClient.Do(metricsReq)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer metricsResp.Body.Close()
	var snapshot struct {
		TotalRequests   uint64 `json:"total_requests"`
		SuccessRequests uint64 `json:"success_requests"`
		TopUsage        []struct {
			AccountID    string `json:"account_id"`
			GatewayKeyID string `json:"gateway_key_id"`
			Requests     uint64 `json:"requests"`
			Successes    uint64 `json:"successes"`
			Errors       uint64 `json:"errors"`
		} `json:"top_usage"`
	}
	if err := json.NewDecoder(metricsResp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if snapshot.TotalRequests != 1 || snapshot.SuccessRequests != 1 {
		t.Fatalf("metrics counters = %#v", snapshot)
	}
	if len(snapshot.TopUsage) != 1 || snapshot.TopUsage[0].AccountID != "acct_claude" || snapshot.TopUsage[0].GatewayKeyID != "default" || snapshot.TopUsage[0].Requests != 1 || snapshot.TopUsage[0].Successes != 1 || snapshot.TopUsage[0].Errors != 0 {
		t.Fatalf("top usage metrics = %#v", snapshot.TopUsage)
	}
}

func TestQueueFGatewayRecordsRuntimeQuotaSwitch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":960}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv, store := gatewayServer(t, upstream.URL)
	defer srv.Close()
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	resp := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status = %d", resp.StatusCode)
	}
	second := doGatewayRequest(t, srv.URL+"/v1/chat/completions", store.GatewayKey(), body)
	_ = second.Body.Close()
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second request should be quota-blocked, status = %d", second.StatusCode)
	}
	cookie := loginCookie(t, srv.URL, "admin-secret")
	metricsReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/metrics", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	metricsReq.AddCookie(cookie)
	metricsResp, err := http.DefaultClient.Do(metricsReq)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer metricsResp.Body.Close()
	var snapshot struct {
		QuotaSwitchCounts map[string]uint64 `json:"quota_switch_counts"`
	}
	if err := json.NewDecoder(metricsResp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if snapshot.QuotaSwitchCounts["acct_1"] != 1 {
		t.Fatalf("quota switch metrics = %#v", snapshot.QuotaSwitchCounts)
	}

	stateReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/dashboard/state", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	stateReq.AddCookie(cookie)
	stateResp, err := http.DefaultClient.Do(stateReq)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer stateResp.Body.Close()
	var state struct {
		Metrics struct {
			QuotaSwitchCounts map[string]uint64 `json:"quota_switch_counts"`
		} `json:"metrics"`
	}
	if err := json.NewDecoder(stateResp.Body).Decode(&state); err != nil {
		t.Fatalf("decode dashboard state: %v", err)
	}
	if state.Metrics.QuotaSwitchCounts["acct_1"] != 1 {
		t.Fatalf("dashboard metrics quota switch = %#v", state.Metrics.QuotaSwitchCounts)
	}
}

func gatewayServer(t *testing.T, upstreamURL string) (*httptest.Server, *config.Store) {
	t.Helper()
	return gatewayServerWithAccounts(t, []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-upstream", BaseURL: upstreamURL, Enabled: true}}, config.GroupRotationPolicy{Strategy: "polling", StickyHeader: "X-Session-ID", RetryOnErrors: false, RotateErrorCodes: []int{429, 401, 403, 404, 500}, CooldownDurationSeconds: 60, MinQuotaThresholdPercent: 0.1})
}

func gatewayServerWithAccounts(t *testing.T, accounts []config.Account, policy config.GroupRotationPolicy) (*httptest.Server, *config.Store) {
	return gatewayServerWithOptions(t, accounts, policy, true)
}

func gatewayServerWithAccountsAndPlatform(t *testing.T, accounts []config.Account, platform string, policy config.GroupRotationPolicy) (*httptest.Server, *config.Store) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = accounts
	cfg.Quota.Policies = []config.QuotaPolicy{{ID: "daily", DailyLimitTokens: 1000, Source: "local"}}
	accountIDs := make([]string, 0, len(cfg.Accounts))
	for i := range cfg.Accounts {
		if strings.TrimSpace(cfg.Accounts[i].QuotaPolicy) == "" {
			cfg.Accounts[i].QuotaPolicy = "daily"
		}
		accountIDs = append(accountIDs, cfg.Accounts[i].ID)
	}
	if platform == "anthropic" {
		cfg.Routing.PreferTiers = []string{"advanced", "simple"}
		cfg.Routing.FallbackTiers = []string{"simple", "advanced"}
	}
	cfg.Groups = []config.Group{{ID: platform, Name: platform, Platform: platform, Status: "active", AccountIDs: accountIDs, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z", RotationPolicy: policy}}
	cfg.GatewayKeys = []config.GatewayKey{{ID: "default", Name: "Default", KeyHash: config.HashGatewayKey("s2a_test_gateway_key_value"), Preview: config.KeyPreview("s2a_test_gateway_key_value"), Status: "enabled", RoutingPolicy: config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{platform}}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z"}}
	cfg.GatewayAuth.GatewayKey = "s2a_test_gateway_key_value"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	handler := server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	return httptest.NewServer(handler), store
}

func gatewayServerWithOptions(t *testing.T, accounts []config.Account, policy config.GroupRotationPolicy, addGroupTag bool) (*httptest.Server, *config.Store) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = accounts
	cfg.Quota.Policies = []config.QuotaPolicy{{ID: "daily", DailyLimitTokens: 1000, Source: "local"}}
	accountIDs := make([]string, 0, len(cfg.Accounts))
	for i := range cfg.Accounts {
		if strings.TrimSpace(cfg.Accounts[i].QuotaPolicy) == "" {
			cfg.Accounts[i].QuotaPolicy = "daily"
		}
		if addGroupTag {
			cfg.Accounts[i].Tags = append(cfg.Accounts[i].Tags, "openai")
		}
		accountIDs = append(accountIDs, cfg.Accounts[i].ID)
	}
	cfg.Groups = []config.Group{{ID: "openai", Name: "OpenAI", Platform: "openai", Status: "active", AccountIDs: accountIDs, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z", RotationPolicy: policy}}
	cfg.GatewayKeys = []config.GatewayKey{{ID: "default", Name: "Default", KeyHash: config.HashGatewayKey("s2a_test_gateway_key_value"), Preview: config.KeyPreview("s2a_test_gateway_key_value"), Status: "enabled", RoutingPolicy: config.KeyRoutingPolicy{Mode: "groups", GroupIDs: []string{"openai"}}, CreatedAt: "1970-01-01T00:00:00Z", UpdatedAt: "1970-01-01T00:00:00Z"}}
	cfg.GatewayAuth.GatewayKey = "s2a_test_gateway_key_value"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	handler := server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	return httptest.NewServer(handler), store
}

func doGatewayRequest(t *testing.T, url string, key string, body []byte) *http.Response {
	return doGatewayRequestWithSession(t, url, key, body, "")
}

func doGatewayRequestWithSession(t *testing.T, url string, key string, body []byte, sessionID string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(http.MethodPost, url, reader)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if sessionID != "" {
		req.Header.Set("X-Session-ID", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

func loginCookie(t *testing.T, serverURL string, password string) *http.Cookie {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	client := http.Client{Jar: jar}
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/admin/login", strings.NewReader(`{"password":"`+password+`"}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == dashboard.CookieName {
			return cookie
		}
	}
	t.Fatal("admin session cookie not found")
	return nil
}
