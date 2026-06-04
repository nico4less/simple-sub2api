package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/dashboard"
	"github.com/0xForce-Network/simple-sub2api/internal/server"
	"github.com/0xForce-Network/simple-sub2api/internal/tunnel"
)

type accountsResponseFixture struct {
	Accounts []struct {
		SubscriptionTier string                    `json:"subscription_tier"`
		UsageInfo        map[string]map[string]any `json:"usage_info"`
	} `json:"accounts"`
}

func TestM0SecurityBoundaries(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Proxies = []config.ProxyConfig{{ID: "proxy_1", URL: "http://127.0.0.1:8081"}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	assertStatus(t, http.MethodGet, srv.URL+"/healthz", nil, nil, http.StatusOK)
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", nil, nil, http.StatusUnauthorized)
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", map[string]string{"Authorization": "Bearer " + store.GatewayKey()}, nil, http.StatusOK)
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", map[string]string{"X-Api-Key": store.GatewayKey()}, nil, http.StatusOK)
	assertStatus(t, http.MethodGet, srv.URL+"/api/admin/me", map[string]string{"Authorization": "Bearer " + store.GatewayKey()}, nil, http.StatusUnauthorized)

	loginResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/login", nil, []byte(`{"password":"admin-secret"}`))
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", loginResp.StatusCode)
	}
	cookie := firstCookie(t, loginResp, dashboard.CookieName)
	assertStatus(t, http.MethodGet, srv.URL+"/api/admin/me", map[string]string{"Cookie": cookie.String()}, nil, http.StatusOK)

	oldKey := store.GatewayKey()
	rotateResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/gateway-key/rotate", map[string]string{"Cookie": cookie.String()}, nil)
	if rotateResp.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d", rotateResp.StatusCode)
	}
	var rotated struct {
		GatewayKey string `json:"gateway_key"`
	}
	if err := json.NewDecoder(rotateResp.Body).Decode(&rotated); err != nil {
		t.Fatalf("decode rotate response: %v", err)
	}
	if rotated.GatewayKey == "" || rotated.GatewayKey == oldKey {
		t.Fatalf("rotate did not issue a fresh key")
	}
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", map[string]string{"Authorization": "Bearer " + oldKey}, nil, http.StatusUnauthorized)
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", map[string]string{"Authorization": "Bearer " + rotated.GatewayKey}, nil, http.StatusOK)
}

func TestE003GatewayKeysCRUDRotateAndAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[],"usage":{"total_tokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "Simple", Tier: "simple", Tags: []string{"code"}, Credential: "api_key=sk-local", BaseURL: upstream.URL, Enabled: true}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	createBody := []byte(`{"config_version":1,"key":{"id":"client_key","name":"Client Key","status":"enabled","routing_policy":{"mode":"tags","tags":["code"]},"note":"local"},"key_value":"s2a_client-test-key-value-000000"}`)
	createResp := doRequest(t, http.MethodPost, srv.URL+"/api/keys", map[string]string{"Cookie": cookie.String()}, createBody)
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("create key status = %d", createResp.StatusCode)
	}
	var created struct {
		ConfigVersion int    `json:"config_version"`
		KeyValue      string `json:"key_value"`
		Key           struct {
			KeyValue string `json:"key_value"`
			KeyHash  string `json:"key_hash"`
			Preview  string `json:"preview"`
		} `json:"key"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created key: %v", err)
	}
	if created.KeyValue != "s2a_client-test-key-value-000000" || created.Key.KeyValue != "" || created.Key.KeyHash != "configured" || created.Key.Preview == "" {
		t.Fatalf("created response = %#v", created)
	}
	if got := store.Snapshot().GatewayKeys[1].KeyHash; strings.Contains(got, created.KeyValue) || !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("stored key hash unsafe: %q", got)
	}
	if got := store.Snapshot().GatewayKeys[1].KeyValue; got != created.KeyValue {
		t.Fatalf("stored key plaintext should remain available for dashboard copy: %q", got)
	}
	keysResp := doRequest(t, http.MethodGet, srv.URL+"/api/keys", map[string]string{"Cookie": cookie.String()}, nil)
	if keysResp.StatusCode != http.StatusOK {
		t.Fatalf("keys status = %d", keysResp.StatusCode)
	}
	var listed struct {
		Keys []struct {
			ID       string `json:"id"`
			KeyValue string `json:"key_value"`
			KeyHash  string `json:"key_hash"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(keysResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode keys: %v", err)
	}
	if len(listed.Keys) != 2 || listed.Keys[1].ID != "client_key" || listed.Keys[1].KeyValue != created.KeyValue || listed.Keys[1].KeyHash != "configured" {
		t.Fatalf("created key list response missing persistent copyable material or redaction: %#v", listed.Keys)
	}

	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`)
	assertStatus(t, http.MethodPost, srv.URL+"/v1/chat/completions", map[string]string{"Authorization": "Bearer " + created.KeyValue}, body, http.StatusOK)
	if store.Snapshot().GatewayKeys[1].LastUsedAt == "" {
		t.Fatalf("last_used_at was not updated")
	}

	disableBody := []byte(`{"config_version":2,"key":{"id":"client_key","name":"Client Key","status":"disabled","routing_policy":{"mode":"tags","tags":["code"]},"preview":"ignored","created_at":"ignored","updated_at":"ignored"}}`)
	disableResp := doRequest(t, http.MethodPut, srv.URL+"/api/keys/client_key", map[string]string{"Cookie": cookie.String()}, disableBody)
	if disableResp.StatusCode != http.StatusOK {
		t.Fatalf("disable key status = %d", disableResp.StatusCode)
	}
	assertStatus(t, http.MethodPost, srv.URL+"/v1/chat/completions", map[string]string{"Authorization": "Bearer " + created.KeyValue}, body, http.StatusUnauthorized)

	enableBody := []byte(`{"config_version":3,"key":{"id":"client_key","name":"Client Key","status":"enabled","routing_policy":{"mode":"tags","tags":["code"]},"preview":"ignored","created_at":"ignored","updated_at":"ignored"}}`)
	enableResp := doRequest(t, http.MethodPut, srv.URL+"/api/keys/client_key", map[string]string{"Cookie": cookie.String()}, enableBody)
	if enableResp.StatusCode != http.StatusOK {
		t.Fatalf("enable key status = %d", enableResp.StatusCode)
	}
	rotateResp := doRequest(t, http.MethodPost, srv.URL+"/api/keys/client_key/rotate", map[string]string{"Cookie": cookie.String()}, []byte(`{"config_version":4}`))
	if rotateResp.StatusCode != http.StatusOK {
		t.Fatalf("rotate key status = %d", rotateResp.StatusCode)
	}
	var rotated struct {
		ConfigVersion int    `json:"config_version"`
		KeyValue      string `json:"key_value"`
	}
	if err := json.NewDecoder(rotateResp.Body).Decode(&rotated); err != nil {
		t.Fatalf("decode rotated key: %v", err)
	}
	assertStatus(t, http.MethodPost, srv.URL+"/v1/chat/completions", map[string]string{"Authorization": "Bearer " + created.KeyValue}, body, http.StatusUnauthorized)
	assertStatus(t, http.MethodPost, srv.URL+"/v1/chat/completions", map[string]string{"Authorization": "Bearer " + rotated.KeyValue}, body, http.StatusOK)
	assertStatus(t, http.MethodDelete, srv.URL+"/api/keys/client_key", map[string]string{"Cookie": cookie.String()}, []byte(`{"config_version":5}`), http.StatusOK)
	assertStatus(t, http.MethodPost, srv.URL+"/v1/chat/completions", map[string]string{"Authorization": "Bearer " + rotated.KeyValue}, body, http.StatusUnauthorized)
}

func TestTunnelAdminStatusAndConfigHotUpdate(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	fakeTunnel := &fakeTunnelController{status: tunnel.RuntimeStatus{Status: tunnel.StatusStopped}}
	srv := httptest.NewServer(server.NewWithOptions(store, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Options{TunnelManager: fakeTunnel}).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	assertStatus(t, http.MethodGet, srv.URL+"/api/admin/tunnel/status", nil, nil, http.StatusUnauthorized)
	statusResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/tunnel/status", map[string]string{"Cookie": cookie.String()}, nil)
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("status endpoint status = %d", statusResp.StatusCode)
	}
	var status tunnel.RuntimeStatus
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatalf("decode tunnel status: %v", err)
	}
	if status.Status != tunnel.StatusStopped {
		t.Fatalf("initial tunnel status = %#v", status)
	}

	body := []byte(`{"config_version":1,"tunnel":{"enabled":true,"mode":"named","binary_path":"/bin/echo","token":"01234567-89ab-cdef-0123-456789abcdef","log_limit_lines":25}}`)
	updateResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/tunnel/config", map[string]string{"Cookie": cookie.String()}, body)
	if updateResp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(updateResp.Body)
		t.Fatalf("update tunnel status = %d body=%s", updateResp.StatusCode, string(payload))
	}
	var updated struct {
		ConfigVersion int                  `json:"config_version"`
		Tunnel        config.TunnelConfig  `json:"tunnel"`
		Status        tunnel.RuntimeStatus `json:"status"`
	}
	if err := json.NewDecoder(updateResp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.ConfigVersion != 2 || !updated.Tunnel.Enabled || updated.Tunnel.Token == "01234567-89ab-cdef-0123-456789abcdef" || !strings.Contains(updated.Tunnel.Token, "...") {
		t.Fatalf("unsafe update response = %#v", updated)
	}
	if updated.Status.Status != tunnel.StatusConnected || updated.Status.PublicURL != "https://fake.trycloudflare.com" {
		t.Fatalf("update status = %#v", updated.Status)
	}
	snapshot := store.Snapshot()
	if snapshot.Tunnel.Token != "01234567-89ab-cdef-0123-456789abcdef" || !snapshot.Tunnel.Enabled || snapshot.ConfigVersion != 2 {
		t.Fatalf("stored tunnel config = %#v version=%d", snapshot.Tunnel, snapshot.ConfigVersion)
	}
	if len(fakeTunnel.applies) != 2 {
		t.Fatalf("apply count = %d, want init plus update", len(fakeTunnel.applies))
	}
	if fakeTunnel.applies[1].cfg.Token != snapshot.Tunnel.Token || fakeTunnel.applies[1].bind != snapshot.Server.Bind {
		t.Fatalf("apply record = %#v", fakeTunnel.applies[1])
	}

	maskedBody := []byte(`{"config_version":2,"tunnel":{"enabled":true,"mode":"named","binary_path":"/bin/echo","token":"0123...cdef","log_limit_lines":30}}`)
	maskedResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/tunnel/config", map[string]string{"Cookie": cookie.String()}, maskedBody)
	if maskedResp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(maskedResp.Body)
		t.Fatalf("masked update status = %d body=%s", maskedResp.StatusCode, string(payload))
	}
	if got := store.Snapshot().Tunnel.Token; got != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("masked token was not preserved: %q", got)
	}

	badResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/tunnel/config", map[string]string{"Cookie": cookie.String()}, []byte(`{"config_version":3,"tunnel":{"enabled":true,"mode":"invalid","log_limit_lines":100}}`))
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid tunnel update status = %d", badResp.StatusCode)
	}
}

func TestE003DefaultGatewayKeyAuthoritativeAndRevealClosed(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.GatewayAuth.GatewayKey = "s2a_legacy-default-key-value-000000"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")
	legacyKey := "s2a_legacy-default-key-value-000000"

	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", map[string]string{"Authorization": "Bearer " + legacyKey}, nil, http.StatusOK)
	assertStatus(t, http.MethodGet, srv.URL+"/api/admin/gateway-key", map[string]string{"Cookie": cookie.String()}, nil, http.StatusGone)
	assertStatus(t, http.MethodPost, srv.URL+"/api/keys/default/reveal", map[string]string{"Cookie": cookie.String()}, nil, http.StatusGone)

	keysResp := doRequest(t, http.MethodGet, srv.URL+"/api/keys", map[string]string{"Cookie": cookie.String()}, nil)
	if keysResp.StatusCode != http.StatusOK {
		t.Fatalf("keys status = %d", keysResp.StatusCode)
	}
	var listed struct {
		Keys []struct {
			ID       string `json:"id"`
			KeyHash  string `json:"key_hash"`
			KeyValue string `json:"key_value"`
			Preview  string `json:"preview"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(keysResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode keys: %v", err)
	}
	if len(listed.Keys) != 1 || listed.Keys[0].ID != "default" || listed.Keys[0].KeyValue != legacyKey || listed.Keys[0].KeyHash != "configured" || listed.Keys[0].Preview == "" {
		t.Fatalf("default key response missing copyable material or redaction: %#v", listed.Keys)
	}

	disableBody := []byte(`{"config_version":1,"key":{"id":"default","name":"Default Gateway Key","status":"disabled","routing_policy":{"mode":"all_enabled"},"preview":"ignored","created_at":"ignored","updated_at":"ignored"}}`)
	assertStatus(t, http.MethodPut, srv.URL+"/api/keys/default", map[string]string{"Cookie": cookie.String()}, disableBody, http.StatusOK)
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", map[string]string{"Authorization": "Bearer " + legacyKey}, nil, http.StatusUnauthorized)

	enableBody := []byte(`{"config_version":2,"key":{"id":"default","name":"Default Gateway Key","status":"enabled","routing_policy":{"mode":"all_enabled"},"preview":"ignored","created_at":"ignored","updated_at":"ignored"}}`)
	assertStatus(t, http.MethodPut, srv.URL+"/api/keys/default", map[string]string{"Cookie": cookie.String()}, enableBody, http.StatusOK)
	assertStatus(t, http.MethodDelete, srv.URL+"/api/keys/default", map[string]string{"Cookie": cookie.String()}, []byte(`{"config_version":3}`), http.StatusOK)
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", map[string]string{"Authorization": "Bearer " + legacyKey}, nil, http.StatusUnauthorized)
	if store.Snapshot().GatewayKeys == nil || len(store.Snapshot().GatewayKeys) != 0 {
		t.Fatalf("gateway_keys should remain authoritative empty collection after delete: %#v", store.Snapshot().GatewayKeys)
	}
}

func TestCORSDefaultClosed(t *testing.T) {
	cfg := config.DefaultConfig()
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	assertStatus(t, http.MethodGet, srv.URL+"/healthz", map[string]string{"Origin": "http://evil.example"}, nil, http.StatusForbidden)
}

func TestCORSExplicitOrigin(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.CORSAllowedOrigins = []string{"http://localhost:3000"}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	resp := doRequest(t, http.MethodGet, srv.URL+"/healthz", map[string]string{"Origin": "http://localhost:3000"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestQueueBImportPreviewAndApply(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	previewResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/import/preview", map[string]string{"Cookie": cookie.String()}, []byte(`{"kind":"line_tokens","content":"sk-preview-token\n","tier":"simple","label":"Preview"}`))
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d", previewResp.StatusCode)
	}
	var preview struct {
		Accounts []struct {
			Credential string `json:"credential"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(previewResp.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(preview.Accounts) != 1 || strings.Contains(preview.Accounts[0].Credential, "sk-preview-token") {
		t.Fatalf("preview credential leaked or missing: %#v", preview.Accounts)
	}
	if len(store.Snapshot().Accounts) != 0 {
		t.Fatal("preview mutated stored accounts")
	}

	applyBody := []byte(`{"config_version":1,"import":{"kind":"line_tokens","content":"sk-preview-token\n","tier":"simple","label":"Preview"}}`)
	applyResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/import/apply", map[string]string{"Cookie": cookie.String()}, applyBody)
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("apply status = %d", applyResp.StatusCode)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Accounts) != 1 || !strings.Contains(snapshot.Accounts[0].Credential, "api_key=sk-preview-token") {
		t.Fatalf("saved accounts = %#v", snapshot.Accounts)
	}
}

func TestQueueBConfigVersionConflict(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")
	body := []byte(`{"config_version":999,"source":{"id":"oauth_1","platform":"openai","type":"token_bundle","label":"OpenAI","tier":"advanced","tags":["code"],"enabled":true,"credentials":{"refresh_token":"secret"},"refresh_state":{"status":"unknown"}}}`)
	resp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/oauth-sources", map[string]string{"Cookie": cookie.String()}, body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestQueueCAdminRuntimeAPIs(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "Simple", Tier: "simple", Credential: "api_key=sk-local", Enabled: true}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	decisionResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/routing/decide", map[string]string{"Cookie": cookie.String()}, []byte(`{"task_type":"document","model":"gpt-4o-mini"}`))
	if decisionResp.StatusCode != http.StatusOK {
		t.Fatalf("routing decide status = %d", decisionResp.StatusCode)
	}
	var decision struct {
		PreferTiers []string `json:"prefer_tiers"`
	}
	if err := json.NewDecoder(decisionResp.Body).Decode(&decision); err != nil {
		t.Fatalf("decode routing decision: %v", err)
	}
	if len(decision.PreferTiers) == 0 || decision.PreferTiers[0] != "simple" {
		t.Fatalf("routing decision = %#v", decision)
	}

	poolResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/account-pool", map[string]string{"Cookie": cookie.String()}, nil)
	if poolResp.StatusCode != http.StatusOK {
		t.Fatalf("account pool status = %d", poolResp.StatusCode)
	}
	var pool struct {
		Accounts []struct {
			AccountID string `json:"account_id"`
			Status    string `json:"status"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(poolResp.Body).Decode(&pool); err != nil {
		t.Fatalf("decode pool: %v", err)
	}
	if len(pool.Accounts) != 1 || pool.Accounts[0].Status != "healthy" {
		t.Fatalf("pool = %#v", pool)
	}

	proxyBody := []byte(`{"config_version":1,"proxy":{"id":"proxy_1","url":"socks5://127.0.0.1:1080"}}`)
	proxyResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/proxies", map[string]string{"Cookie": cookie.String()}, proxyBody)
	if proxyResp.StatusCode != http.StatusOK {
		t.Fatalf("proxy status = %d", proxyResp.StatusCode)
	}
	if got := store.Snapshot().Proxies[0].URL; !strings.HasPrefix(got, "socks5h://") {
		t.Fatalf("proxy URL was not normalized to socks5h: %q", got)
	}
}

func TestQueueCFailedProxyUpdatePreservesStoredConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "Simple", Tier: "simple", Credential: "api_key=sk-local", Enabled: true}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	body := []byte(`{"config_version":1,"proxy":{"id":"bad_proxy","url":"ftp://127.0.0.1:21"}}`)
	resp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/proxies", map[string]string{"Cookie": cookie.String()}, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad proxy status = %d", resp.StatusCode)
	}
	snapshot := store.Snapshot()
	if snapshot.ConfigVersion != 1 || len(snapshot.Proxies) != 0 || len(snapshot.Accounts) != 1 {
		t.Fatalf("stored config mutated after failed update: %#v", snapshot)
	}
}

func TestQueueELoginAndTwoPageIAShell(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "Simple", Tier: "simple", Credential: "api_key=sk-local", Enabled: true}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	rootResp := doRequest(t, http.MethodGet, srv.URL+"/", map[string]string{"X-Test-No-Redirect": "1"}, nil)
	if rootResp.StatusCode != http.StatusFound || rootResp.Header.Get("Location") != "/login" {
		t.Fatalf("unauthenticated root redirect = %d %q", rootResp.StatusCode, rootResp.Header.Get("Location"))
	}
	unauthDashboard := doRequest(t, http.MethodGet, srv.URL+"/dashboard", map[string]string{"X-Test-No-Redirect": "1"}, nil)
	if unauthDashboard.StatusCode != http.StatusFound || unauthDashboard.Header.Get("Location") != "/login" {
		t.Fatalf("unauthenticated dashboard redirect = %d %q", unauthDashboard.StatusCode, unauthDashboard.Header.Get("Location"))
	}
	loginShellResp := doRequest(t, http.MethodGet, srv.URL+"/login", nil, nil)
	if loginShellResp.StatusCode != http.StatusOK {
		t.Fatalf("login shell status = %d", loginShellResp.StatusCode)
	}
	loginShellBody, err := io.ReadAll(loginShellResp.Body)
	if err != nil {
		t.Fatalf("read login shell: %v", err)
	}
	loginHTML := string(loginShellBody)
	for _, forbidden := range []string{"Username", "Register", "Password reset", "OAuth", "captcha", "TOTP", "Invite"} {
		if strings.Contains(loginHTML, forbidden) {
			t.Fatalf("login shell contains out-of-scope marker %q", forbidden)
		}
	}
	for _, marker := range []string{"Simple Sub2API Console", "module", "/assets/"} {
		if !strings.Contains(loginHTML, marker) {
			t.Fatalf("login shell missing marker %q", marker)
		}
	}

	cookie := loginCookie(t, srv.URL, "admin-secret")
	authLogin := doRequest(t, http.MethodGet, srv.URL+"/login", map[string]string{"Cookie": cookie.String(), "X-Test-No-Redirect": "1"}, nil)
	if authLogin.StatusCode != http.StatusFound || authLogin.Header.Get("Location") != "/dashboard" {
		t.Fatalf("authenticated login redirect = %d %q", authLogin.StatusCode, authLogin.Header.Get("Location"))
	}
	shellResp := doRequest(t, http.MethodGet, srv.URL+"/dashboard", map[string]string{"Cookie": cookie.String()}, nil)
	if shellResp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard shell status = %d", shellResp.StatusCode)
	}
	shellBody, err := io.ReadAll(shellResp.Body)
	if err != nil {
		t.Fatalf("read dashboard shell: %v", err)
	}
	shellHTML := string(shellBody)
	if !strings.Contains(shellHTML, "Simple Sub2API Console") || strings.Contains(shellHTML, "https://") {
		t.Fatalf("dashboard shell missing title or contains external asset reference")
	}
	for _, marker := range []string{"Simple Sub2API Console", "module", "/assets/"} {
		if !strings.Contains(shellHTML, marker) {
			t.Fatalf("dashboard shell missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{"OAuth Sources", "Routing Edit / Test", "Proxy Config / Probe", "Debug Output", "refreshOAuth", "reauthOAuth"} {
		if strings.Contains(shellHTML, forbidden) {
			t.Fatalf("dashboard shell still contains non-E000 marker %q", forbidden)
		}
	}
	accountsResp := doRequest(t, http.MethodGet, srv.URL+"/admin/accounts", map[string]string{"Cookie": cookie.String()}, nil)
	if accountsResp.StatusCode != http.StatusOK {
		t.Fatalf("accounts shell status = %d", accountsResp.StatusCode)
	}
	keysAliasResp := doRequest(t, http.MethodGet, srv.URL+"/keys", map[string]string{"Cookie": cookie.String()}, nil)
	if keysAliasResp.StatusCode != http.StatusOK {
		t.Fatalf("keys alias shell status = %d", keysAliasResp.StatusCode)
	}
	assetResp := doRequest(t, http.MethodGet, srv.URL+"/assets/index.js", nil, nil)
	if assetResp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard asset status = %d", assetResp.StatusCode)
	}

	assertStatus(t, http.MethodGet, srv.URL+"/api/admin/dashboard/state", nil, nil, http.StatusUnauthorized)
	stateResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/dashboard/state", map[string]string{"Cookie": cookie.String()}, nil)
	if stateResp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard state status = %d", stateResp.StatusCode)
	}
	var state struct {
		Config struct {
			Accounts []struct {
				Credential string `json:"credential"`
			} `json:"accounts"`
		} `json:"config"`
		AccountPool struct {
			Accounts []struct {
				AccountID string `json:"account_id"`
			} `json:"accounts"`
		} `json:"account_pool"`
		Metrics struct {
			TotalRequests uint64 `json:"total_requests"`
		} `json:"metrics"`
	}
	if err := json.NewDecoder(stateResp.Body).Decode(&state); err != nil {
		t.Fatalf("decode dashboard state: %v", err)
	}
	if len(state.Config.Accounts) != 1 || strings.Contains(state.Config.Accounts[0].Credential, "sk-local") {
		t.Fatalf("dashboard state leaked credential: %#v", state.Config.Accounts)
	}
	if len(state.AccountPool.Accounts) != 1 || state.AccountPool.Accounts[0].AccountID != "acct_1" {
		t.Fatalf("dashboard account pool = %#v", state.AccountPool)
	}
	if state.Metrics.TotalRequests != 0 {
		t.Fatalf("fresh dashboard metrics = %#v", state.Metrics)
	}
}

func TestQueueFMetricsAPIIsAdminOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Metrics.RecentErrorsLimit = 3
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	assertStatus(t, http.MethodGet, srv.URL+"/api/admin/metrics", nil, nil, http.StatusUnauthorized)
	cookie := loginCookie(t, srv.URL, "admin-secret")
	metricsResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/metrics", map[string]string{"Cookie": cookie.String()}, nil)
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d", metricsResp.StatusCode)
	}
	var snapshot struct {
		StartedAt     string  `json:"started_at"`
		QPS           float64 `json:"qps"`
		TotalRequests uint64  `json:"total_requests"`
		RecentErrors  []struct {
			Message string `json:"message"`
		} `json:"recent_errors"`
	}
	if err := json.NewDecoder(metricsResp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if snapshot.StartedAt == "" || snapshot.QPS != 0 || snapshot.TotalRequests != 0 || len(snapshot.RecentErrors) != 0 {
		t.Fatalf("unexpected fresh metrics snapshot: %#v", snapshot)
	}
}

func TestRCHardeningDebugSnapshotAdminOnlyDisabledAndSanitized(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret-sentinel"
	cfg.GatewayAuth.GatewayKey = "s2a_gateway-secret-sentinel-value"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "Simple", Tier: "simple", Credential: "api_key=account-secret-sentinel", Enabled: true}}
	cfg.OAuthSources = []config.OAuthSource{{ID: "oauth_1", Platform: "openai", Type: "token_bundle", Label: "OAuth", Tier: "advanced", Enabled: true, Credentials: map[string]string{"refresh_token": "oauth-secret-sentinel"}, RefreshState: config.RefreshState{Status: "unknown"}}}
	cfg.SubscriptionSources = []config.SubscriptionSource{{ID: "sub_1", Kind: "inline_bundle", Label: "Inline", Tier: "simple", Enabled: true, InlineBundle: "inline-secret-sentinel"}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	disabledSrv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer disabledSrv.Close()

	assertStatus(t, http.MethodGet, disabledSrv.URL+"/api/admin/debug/snapshot", nil, nil, http.StatusUnauthorized)
	cookie := loginCookie(t, disabledSrv.URL, "admin-secret-sentinel")
	disabledResp := doRequest(t, http.MethodGet, disabledSrv.URL+"/api/admin/debug/snapshot", map[string]string{"Cookie": cookie.String()}, nil)
	if disabledResp.StatusCode != http.StatusOK {
		t.Fatalf("disabled debug status = %d", disabledResp.StatusCode)
	}
	var disabled struct {
		Enabled bool   `json:"enabled"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(disabledResp.Body).Decode(&disabled); err != nil {
		t.Fatalf("decode disabled debug: %v", err)
	}
	if disabled.Enabled || disabled.Status != "disabled" {
		t.Fatalf("disabled debug snapshot leaked state: %#v", disabled)
	}

	enabledSrv := httptest.NewServer(server.NewWithOptions(store, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Options{DebugDashboard: true}).Handler())
	defer enabledSrv.Close()
	enabledCookie := loginCookie(t, enabledSrv.URL, "admin-secret-sentinel")
	enabledResp := doRequest(t, http.MethodGet, enabledSrv.URL+"/api/admin/debug/snapshot", map[string]string{"Cookie": enabledCookie.String()}, nil)
	if enabledResp.StatusCode != http.StatusOK {
		t.Fatalf("enabled debug status = %d", enabledResp.StatusCode)
	}
	body, err := io.ReadAll(enabledResp.Body)
	if err != nil {
		t.Fatalf("read enabled debug body: %v", err)
	}
	var snapshot struct {
		Enabled       bool `json:"enabled"`
		ConfigSummary struct {
			Accounts            int `json:"accounts"`
			OAuthSources        int `json:"oauth_sources"`
			SubscriptionSources int `json:"subscription_sources"`
			EnabledAccounts     int `json:"enabled_accounts"`
		} `json:"config_summary"`
	}
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatalf("decode enabled debug: %v", err)
	}
	if !snapshot.Enabled || snapshot.ConfigSummary.Accounts != 1 || snapshot.ConfigSummary.EnabledAccounts != 1 || snapshot.ConfigSummary.OAuthSources != 1 || snapshot.ConfigSummary.SubscriptionSources != 1 {
		t.Fatalf("enabled debug snapshot summary = %#v", snapshot)
	}
	for _, sentinel := range []string{"admin-secret-sentinel", "gateway-secret-sentinel", "account-secret-sentinel", "oauth-secret-sentinel", "inline-secret-sentinel", "Authorization", "Cookie"} {
		if strings.Contains(string(body), sentinel) {
			t.Fatalf("debug snapshot leaked sentinel %q in %s", sentinel, string(body))
		}
	}
}

func TestQueueEConfigSaveConflictAndRedactedSecretPreservation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "Simple", Tier: "simple", Credential: "api_key=sk-local", Enabled: true}}
	cfg.OAuthSources = []config.OAuthSource{{ID: "oauth_1", Platform: "openai", Type: "token_bundle", Label: "OAuth", Tier: "advanced", Enabled: true, Credentials: map[string]string{"refresh_token": "rt-secret"}, RefreshState: config.RefreshState{Status: "unknown"}}}
	cfg.SubscriptionSources = []config.SubscriptionSource{{ID: "sub_1", Kind: "inline_bundle", Label: "Inline", Tier: "simple", Enabled: true, InlineBundle: "inline-secret"}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	stateResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/dashboard/state", map[string]string{"Cookie": cookie.String()}, nil)
	var state struct {
		Config config.Config `json:"config"`
	}
	if err := json.NewDecoder(stateResp.Body).Decode(&state); err != nil {
		t.Fatalf("decode dashboard state: %v", err)
	}
	state.Config.Accounts[0].Label = "Renamed"
	body, err := json.Marshal(map[string]any{"config_version": 999, "config": state.Config})
	if err != nil {
		t.Fatalf("marshal stale save: %v", err)
	}
	conflictResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/config/save", map[string]string{"Cookie": cookie.String()}, body)
	if conflictResp.StatusCode != http.StatusConflict {
		t.Fatalf("config save conflict status = %d", conflictResp.StatusCode)
	}

	body, err = json.Marshal(map[string]any{"config_version": state.Config.ConfigVersion, "config": state.Config})
	if err != nil {
		t.Fatalf("marshal save: %v", err)
	}
	saveResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/config/save", map[string]string{"Cookie": cookie.String()}, body)
	if saveResp.StatusCode != http.StatusOK {
		t.Fatalf("config save status = %d", saveResp.StatusCode)
	}
	snapshot := store.Snapshot()
	if snapshot.Accounts[0].Label != "Renamed" || snapshot.Accounts[0].Credential != "api_key=sk-local" {
		t.Fatalf("config save did not preserve secret/update label: %#v", snapshot.Accounts[0])
	}
	if snapshot.GatewayAuth.GatewayKey == "" || strings.Contains(snapshot.GatewayAuth.GatewayKey, "...") {
		t.Fatalf("gateway key was not preserved: %q", snapshot.GatewayAuth.GatewayKey)
	}
	if snapshot.Dashboard.AdminPassword != "admin-secret" {
		t.Fatalf("admin password was not preserved: %q", snapshot.Dashboard.AdminPassword)
	}
	if snapshot.OAuthSources[0].Credentials["refresh_token"] != "rt-secret" {
		t.Fatalf("oauth credential was not preserved: %#v", snapshot.OAuthSources[0].Credentials)
	}
	if snapshot.SubscriptionSources[0].InlineBundle != "inline-secret" {
		t.Fatalf("inline bundle was not preserved: %q", snapshot.SubscriptionSources[0].InlineBundle)
	}
}

func TestQueueEOAuthRefreshAndReauth(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.OAuthSources = []config.OAuthSource{{ID: "oauth_1", Platform: "openai", Type: "token_bundle", Label: "OAuth", Tier: "advanced", Enabled: true, Credentials: map[string]string{"refresh_token": "rt-secret"}, RefreshState: config.RefreshState{Status: "unknown"}}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")
	assertStatus(t, http.MethodPost, srv.URL+"/api/admin/oauth-sources/oauth_1/refresh", nil, []byte(`{"config_version":1}`), http.StatusUnauthorized)

	refreshResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/oauth-sources/oauth_1/refresh", map[string]string{"Cookie": cookie.String()}, []byte(`{"config_version":1}`))
	if refreshResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d", refreshResp.StatusCode)
	}
	if got := store.Snapshot().OAuthSources[0].RefreshState.Status; got != "ok" {
		t.Fatalf("refresh state = %q", got)
	}
	reauthResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/oauth-sources/oauth_1/reauth", map[string]string{"Cookie": cookie.String()}, []byte(`{"config_version":2}`))
	if reauthResp.StatusCode != http.StatusOK {
		t.Fatalf("reauth status = %d", reauthResp.StatusCode)
	}
	if got := store.Snapshot().OAuthSources[0].RefreshState.Status; got != "needs_auth" {
		t.Fatalf("reauth state = %q", got)
	}
}

func TestQueueEAccountDeleteAndRefresh(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "Simple", Tier: "simple", Credential: "api_key=sk-local", Enabled: true}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	refreshResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/accounts/acct_1/refresh", map[string]string{"Cookie": cookie.String()}, nil)
	if refreshResp.StatusCode != http.StatusOK {
		t.Fatalf("account refresh status = %d", refreshResp.StatusCode)
	}
	deleteResp := doRequest(t, http.MethodDelete, srv.URL+"/api/admin/accounts/acct_1", map[string]string{"Cookie": cookie.String()}, []byte(`{"config_version":1}`))
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("account delete status = %d", deleteResp.StatusCode)
	}
	if len(store.Snapshot().Accounts) != 0 {
		t.Fatalf("account was not deleted: %#v", store.Snapshot().Accounts)
	}
}

func TestAccountCheckAllSyncsOpenAIAccountCoreDisplayState(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/accounts/check/v4-2023-04-27":
			writeFixtureJSON(t, w, map[string]any{"accounts": map[string]any{"acct": map[string]any{"account": map[string]any{"plan_type": "team", "is_default": true}}}})
		case "/backend-api/codex/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("x-codex-primary-used-percent", "91")
			w.Header().Set("x-codex-primary-reset-after-seconds", "604800")
			w.Header().Set("x-codex-primary-window-minutes", "10080")
			w.Header().Set("x-codex-secondary-used-percent", "27")
			w.Header().Set("x-codex-secondary-reset-after-seconds", "18000")
			w.Header().Set("x-codex-secondary-window-minutes", "300")
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_oauth", Type: "oauth", Label: "OAuth", Tier: "simple", Credential: "access_token=chatgpt-access;refresh_token=rt-test;chatgpt_account_id=acct", Metadata: map[string]any{"platform": "openai", "account_category": "oauth-based"}, Enabled: true}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.NewWithOptions(store, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Options{OpenAICodexBaseURL: upstream.URL}).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	resp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/account-check", map[string]string{"Cookie": cookie.String()}, nil)
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("account-check status = %d body=%s", resp.StatusCode, string(payload))
	}
	_ = resp.Body.Close()
	accountsResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/accounts", map[string]string{"Cookie": cookie.String()}, nil)
	if accountsResp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(accountsResp.Body)
		t.Fatalf("accounts status = %d body=%s", accountsResp.StatusCode, string(payload))
	}
	var decoded accountsResponseFixture
	if err := json.NewDecoder(accountsResp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode accounts response: %v", err)
	}
	if len(decoded.Accounts) != 1 {
		t.Fatalf("accounts response length = %d", len(decoded.Accounts))
	}
	if decoded.Accounts[0].SubscriptionTier != "team" {
		t.Fatalf("subscription tier = %q", decoded.Accounts[0].SubscriptionTier)
	}
	if got := decoded.Accounts[0].UsageInfo["five_hour"]["used_percent"]; got != float64(27) {
		t.Fatalf("five_hour used_percent = %#v", got)
	}
	if got := decoded.Accounts[0].UsageInfo["seven_day"]["used_percent"]; got != float64(91) {
		t.Fatalf("seven_day used_percent = %#v", got)
	}
}

func TestE002AccountsCoreCRUDImportExportAndPoolDisable(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Proxies = []config.ProxyConfig{{ID: "proxy_1", URL: "http://127.0.0.1:8081"}}
	cfg.Quota.Policies = []config.QuotaPolicy{{ID: "quota_1", Source: "manual", DailyLimitTokens: 1000}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFixtureJSON(t, w, map[string]any{"object": "list", "data": []any{}})
	}))
	defer upstream.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	createBody, err := json.Marshal(map[string]any{
		"config_version": 1,
		"account": map[string]any{
			"id":           "acct_core",
			"type":         "openai_api_key",
			"label":        "Core Account",
			"tier":         "simple",
			"tags":         []string{"primary", "manual"},
			"credential":   "api_key=sk-core-secret",
			"base_url":     upstream.URL,
			"model":        "gpt-test",
			"quota_policy": "quota_1",
			"enabled":      true,
		},
	})
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	createResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/accounts", map[string]string{"Cookie": cookie.String()}, createBody)
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", createResp.StatusCode)
	}
	if got := store.Snapshot().Accounts[0].Credential; got != "api_key=sk-core-secret" {
		t.Fatalf("stored credential = %q", got)
	}

	listResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/accounts", map[string]string{"Cookie": cookie.String()}, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", listResp.StatusCode)
	}
	listBody, err := io.ReadAll(listResp.Body)
	if err != nil {
		t.Fatalf("read list body: %v", err)
	}
	if strings.Contains(string(listBody), "sk-core-secret") || !strings.Contains(string(listBody), "Core Account") || !strings.Contains(string(listBody), "proxy_1") {
		t.Fatalf("list response did not redact/include expected account fields: %s", string(listBody))
	}

	var listed struct {
		ConfigVersion int `json:"config_version"`
		Accounts      []struct {
			Config struct {
				ID         string   `json:"id"`
				Credential string   `json:"credential"`
				Tags       []string `json:"tags"`
			} `json:"config"`
			RuntimeStatus string `json:"runtime_status"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(listBody, &listed); err != nil {
		t.Fatalf("decode accounts list: %v", err)
	}
	if listed.ConfigVersion != 2 || len(listed.Accounts) != 1 || listed.Accounts[0].RuntimeStatus != "healthy" || strings.Contains(listed.Accounts[0].Config.Credential, "sk-core-secret") {
		t.Fatalf("unexpected accounts list: %#v", listed)
	}

	updateBody := []byte(`{"config_version":2,"account":{"id":"acct_core","type":"openai_api_key","label":"Core Account Disabled","tier":"simple","tags":["disabled"],"credential":"redacted","model":"gpt-test","enabled":false}}`)
	updateResp := doRequest(t, http.MethodPut, srv.URL+"/api/admin/accounts/acct_core", map[string]string{"Cookie": cookie.String()}, updateBody)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", updateResp.StatusCode)
	}
	snapshot := store.Snapshot()
	if snapshot.Accounts[0].Credential != "api_key=sk-core-secret" || snapshot.Accounts[0].Enabled {
		t.Fatalf("update failed to preserve credential or disable account: %#v", snapshot.Accounts[0])
	}
	assertStatus(t, http.MethodPost, srv.URL+"/api/admin/routing/decide", map[string]string{"Cookie": cookie.String()}, []byte(`{"task_type":"document","model":"gpt-4o-mini"}`), http.StatusOK)
	assertStatus(t, http.MethodPost, srv.URL+"/v1/chat/completions", map[string]string{"Authorization": "Bearer " + store.GatewayKey()}, []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`), http.StatusServiceUnavailable)

	testResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/accounts/acct_core/test", map[string]string{"Cookie": cookie.String()}, nil)
	if testResp.StatusCode != http.StatusOK {
		t.Fatalf("test status = %d", testResp.StatusCode)
	}
	var testResult struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(testResp.Body).Decode(&testResult); err != nil {
		t.Fatalf("decode test result: %v", err)
	}
	if testResult.Status != "disabled" {
		t.Fatalf("disabled account test result = %#v", testResult)
	}

	previewResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/accounts/import/preview", map[string]string{"Cookie": cookie.String()}, []byte(`{"kind":"line_tokens","content":"sk-imported-secret\n","tier":"simple","label":"E002 Import"}`))
	if previewResp.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d", previewResp.StatusCode)
	}
	previewBody, err := io.ReadAll(previewResp.Body)
	if err != nil {
		t.Fatalf("read preview body: %v", err)
	}
	if strings.Contains(string(previewBody), "sk-imported-secret") {
		t.Fatalf("preview leaked import secret: %s", string(previewBody))
	}
	applyResp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/accounts/import/apply", map[string]string{"Cookie": cookie.String()}, []byte(`{"config_version":3,"import":{"kind":"line_tokens","content":"sk-imported-secret\n","tier":"simple","label":"E002 Import"}}`))
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("apply status = %d", applyResp.StatusCode)
	}
	if len(store.Snapshot().Accounts) != 2 {
		t.Fatalf("import did not add account: %#v", store.Snapshot().Accounts)
	}

	exportResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/accounts/export", map[string]string{"Cookie": cookie.String()}, nil)
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d", exportResp.StatusCode)
	}
	exportBody, err := io.ReadAll(exportResp.Body)
	if err != nil {
		t.Fatalf("read export body: %v", err)
	}
	if strings.Contains(string(exportBody), "sk-core-secret") || strings.Contains(string(exportBody), "sk-imported-secret") || !strings.Contains(string(exportBody), "accounts") {
		t.Fatalf("export did not redact or include accounts: %s", string(exportBody))
	}
}

func TestAccountsCreateAcceptsUpstreamCreatePayloadAsJSONAccount(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Proxies = []config.ProxyConfig{{ID: "proxy_1", URL: "http://127.0.0.1:8081"}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	body := []byte(`{"config_version":1,"name":"Claude Console","notes":"from upstream form","platform":"anthropic","type":"apikey","credentials":{"base_url":"https://api.anthropic.com","api_key":"sk-ant-api03-test","model_mapping":{"claude-sonnet":"claude-sonnet-4-6"}},"extra":{"anthropic_passthrough":true,"allowed_models":["claude-sonnet-4-6"]},"proxy_id":"proxy_1","concurrency":2,"load_factor":3,"priority":4,"group_ids":["claude"],"expires_at":1893456000,"auto_pause_on_expired":true}`)
	resp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/accounts", map[string]string{"Cookie": cookie.String()}, body)
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("create upstream payload status = %d body = %s", resp.StatusCode, string(data))
	}

	snapshot := store.Snapshot()
	if len(snapshot.Accounts) != 1 {
		t.Fatalf("stored account count = %d", len(snapshot.Accounts))
	}
	account := snapshot.Accounts[0]
	if account.Label != "Claude Console" || account.Type != "openai_compatible" || account.BaseURL != "https://api.anthropic.com" || account.ProxyRef != "proxy_1" || !account.Enabled {
		t.Fatalf("unexpected stored account: %#v", account)
	}
	if !strings.Contains(account.Credential, "api_key=sk-ant-api03-test") || !strings.Contains(account.Credential, "base_url=https://api.anthropic.com") {
		t.Fatalf("credential was not converted to local envelope: %q", account.Credential)
	}
	if account.Metadata["platform"] != "anthropic" || account.Metadata["account_category"] != "apikey" || account.Metadata["notes"] != "from upstream form" {
		t.Fatalf("unexpected metadata identity: %#v", account.Metadata)
	}
	rows, ok := account.Metadata["model_mappings"].([]map[string]string)
	if !ok || len(rows) != 1 || rows[0]["from"] != "claude-sonnet" || rows[0]["to"] != "claude-sonnet-4-6" {
		t.Fatalf("unexpected editable model mapping rows: %#v", account.Metadata["model_mappings"])
	}
	if account.Metadata["concurrency"] != 2 || account.Metadata["priority"] != 4 {
		t.Fatalf("unexpected scheduling metadata: %#v", account.Metadata)
	}
	groups, ok := account.Metadata["group_ids"].([]string)
	if !ok || len(groups) != 1 || groups[0] != "claude" {
		t.Fatalf("unexpected group metadata: %#v", account.Metadata["group_ids"])
	}
}

func TestE004DeleteAccountPrunesGatewayPolicyAndRecentUsageIsRedacted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "A", Tier: "simple", Credential: "api_key=sk-upstream", BaseURL: upstream.URL, Enabled: true}}
	cfg.Groups[0].AccountIDs = []string{"acct_1"}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	createKeyBody := []byte(`{"config_version":1,"key":{"id":"acct_only","name":"Account Only","status":"enabled","routing_policy":{"mode":"account_ids","account_ids":["acct_1"]}},"key_value":"s2a_custom-account-only-key-000000"}`)
	createKeyResp := doRequest(t, http.MethodPost, srv.URL+"/api/keys", map[string]string{"Cookie": cookie.String()}, createKeyBody)
	if createKeyResp.StatusCode != http.StatusOK {
		t.Fatalf("create key status = %d", createKeyResp.StatusCode)
	}
	assertStatus(t, http.MethodPost, srv.URL+"/v1/chat/completions", map[string]string{"Authorization": "Bearer s2a_custom-account-only-key-000000"}, []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`), http.StatusOK)
	versionAfterUsage := store.Snapshot().ConfigVersion

	metricsResp := doRequest(t, http.MethodGet, srv.URL+"/api/admin/dashboard/recent-usage", map[string]string{"Cookie": cookie.String()}, nil)
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("recent usage status = %d", metricsResp.StatusCode)
	}
	metricsBody, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatalf("read recent usage body: %v", err)
	}
	if strings.Contains(string(metricsBody), "s2a_custom-account-only-key-000000") || strings.Contains(string(metricsBody), "sk-upstream") {
		t.Fatalf("recent usage leaked secret material: %s", string(metricsBody))
	}
	if !strings.Contains(string(metricsBody), "acct_1") || !strings.Contains(string(metricsBody), "acct_only") {
		t.Fatalf("recent usage missing expected identifiers: %s", string(metricsBody))
	}
	if !strings.Contains(string(metricsBody), "\"rank\":1") || !strings.Contains(string(metricsBody), "\"success_rate\":1") || !strings.Contains(string(metricsBody), "Account Only") {
		t.Fatalf("recent usage missing rank, success rate, or labels: %s", string(metricsBody))
	}

	deleteBody, err := json.Marshal(map[string]any{"config_version": versionAfterUsage})
	if err != nil {
		t.Fatalf("marshal delete body: %v", err)
	}
	deleteResp := doRequest(t, http.MethodDelete, srv.URL+"/api/admin/accounts/acct_1", map[string]string{"Cookie": cookie.String()}, deleteBody)
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", deleteResp.StatusCode)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Accounts) != 0 || len(snapshot.GatewayKeys) != 2 || len(snapshot.GatewayKeys[1].RoutingPolicy.AccountIDs) != 0 || len(snapshot.Groups[0].AccountIDs) != 0 {
		t.Fatalf("account delete did not prune references: %#v", snapshot)
	}
}

func TestAccountCreateIgnoresMissingTunnelBinary(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Tunnel.Enabled = true
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	fakeTunnel := &fakeTunnelController{applyErr: fmt.Errorf("cloudflared binary not found: %w", exec.ErrNotFound)}
	srv := httptest.NewServer(server.NewWithOptions(store, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Options{TunnelManager: fakeTunnel}).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")

	body := []byte(`{"config_version":1,"account":{"id":"acct_oauth","type":"oauth","label":"OAuth","tier":"simple","credential":"refresh_token=rt-test","metadata":{"platform":"openai","account_category":"oauth-based"},"enabled":true}}`)
	resp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/accounts", map[string]string{"Cookie": cookie.String()}, body)
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("create account status = %d body=%s", resp.StatusCode, string(responseBody))
	}
	snapshot := store.Snapshot()
	if len(snapshot.Accounts) != 1 || snapshot.Accounts[0].ID != "acct_oauth" {
		t.Fatalf("account was not persisted when tunnel binary was missing: %#v", snapshot.Accounts)
	}
	if len(fakeTunnel.applies) == 0 {
		t.Fatal("expected tunnel Apply attempt")
	}
}

func TestOpenAIOAuthExchangeCodeReturnsTokenBundle(t *testing.T) {
	var gotForm map[string]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			gotForm = map[string]string{
				"grant_type":    r.Form.Get("grant_type"),
				"client_id":     r.Form.Get("client_id"),
				"code":          r.Form.Get("code"),
				"redirect_uri":  r.Form.Get("redirect_uri"),
				"code_verifier": r.Form.Get("code_verifier"),
			}
			writeFixtureJSON(t, w, map[string]any{
				"access_token":  "access-from-code",
				"refresh_token": "refresh-from-code",
				"id_token":      "id-from-code",
				"expires_in":    3600,
				"token_type":    "Bearer",
			})
		case "/backend-api/accounts/check/v4-2023-04-27":
			writeFixtureJSON(t, w, map[string]any{"accounts": map[string]any{"acct": map[string]any{"account": map[string]any{"plan_type": "plus", "is_default": true}}}})
		case "/backend-api/codex/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("x-codex-primary-used-percent", "88")
			w.Header().Set("x-codex-primary-reset-after-seconds", "604800")
			w.Header().Set("x-codex-primary-window-minutes", "10080")
			w.Header().Set("x-codex-secondary-used-percent", "42")
			w.Header().Set("x-codex-secondary-reset-after-seconds", "18000")
			w.Header().Set("x-codex-secondary-window-minutes", "300")
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.NewWithOptions(store, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Options{OpenAICodexBaseURL: upstream.URL}).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")
	body := []byte(`{"code":"auth-code-value","state":"state-value","code_verifier":"verifier-value","redirect_uri":"http://localhost:1455/auth/callback","client_id":"app_EMoamEEZ73f0CkXaXp7hrann","token_url":"` + upstream.URL + `/oauth/token"}`)
	resp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/openai/oauth/exchange-code", map[string]string{"Cookie": cookie.String()}, body)
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("exchange status = %d body=%s", resp.StatusCode, string(payload))
	}
	var decoded struct {
		Credentials map[string]string `json:"credentials"`
		ExpiresAt   string            `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode exchange response: %v", err)
	}
	if decoded.Credentials["refresh_token"] != "refresh-from-code" || decoded.Credentials["access_token"] != "access-from-code" || decoded.Credentials["id_token"] != "id-from-code" || decoded.Credentials["client_id"] == "" || decoded.ExpiresAt == "" {
		t.Fatalf("unexpected token bundle: %#v", decoded)
	}
	if decoded.Credentials["subscription_tier"] != "plus" || decoded.Credentials["codex_5h_used_percent"] != "42" || decoded.Credentials["codex_7d_used_percent"] != "88" {
		t.Fatalf("expected plan and codex usage in token bundle: %#v", decoded.Credentials)
	}
	if gotForm["grant_type"] != "authorization_code" || gotForm["code"] != "auth-code-value" || gotForm["code_verifier"] != "verifier-value" || gotForm["redirect_uri"] != "http://localhost:1455/auth/callback" {
		t.Fatalf("unexpected exchange form: %#v", gotForm)
	}
}

func TestClaudeOAuthExchangeCodeReturnsTokenBundle(t *testing.T) {
	var gotPayload map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if r.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
			}
			if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
				t.Fatalf("decode token payload: %v", err)
			}
			writeFixtureJSON(t, w, map[string]any{
				"access_token":  "claude-access-from-code",
				"refresh_token": "claude-refresh-from-code",
				"expires_in":    3600,
				"token_type":    "Bearer",
				"scope":         "user:inference",
				"organization":  map[string]any{"uuid": "org-uuid-1"},
				"account":       map[string]any{"uuid": "acct-uuid-1", "email_address": "claude@example.test"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.NewWithOptions(store, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Options{}).Handler())
	defer srv.Close()
	cookie := loginCookie(t, srv.URL, "admin-secret")
	body := []byte(`{"code":"auth-code-value#state-from-code","code_verifier":"verifier-value","token_url":"` + upstream.URL + `/oauth/token","is_setup_token":true}`)
	resp := doRequest(t, http.MethodPost, srv.URL+"/api/admin/claude/oauth/exchange-code", map[string]string{"Cookie": cookie.String()}, body)
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("exchange status = %d body=%s", resp.StatusCode, string(payload))
	}
	var decoded struct {
		Credentials map[string]string `json:"credentials"`
		ExpiresAt   string            `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode exchange response: %v", err)
	}
	if decoded.Credentials["refresh_token"] != "claude-refresh-from-code" || decoded.Credentials["access_token"] != "claude-access-from-code" || decoded.Credentials["expires_at"] == "" {
		t.Fatalf("unexpected token bundle: %#v", decoded)
	}
	if decoded.Credentials["token_type"] != "Bearer" || decoded.Credentials["scope"] != "user:inference" || decoded.Credentials["org_uuid"] != "org-uuid-1" || decoded.Credentials["account_uuid"] != "acct-uuid-1" || decoded.Credentials["email_address"] != "claude@example.test" {
		t.Fatalf("missing Claude token metadata: %#v", decoded.Credentials)
	}
	if gotPayload["grant_type"] != "authorization_code" || gotPayload["code"] != "auth-code-value" || gotPayload["state"] != "state-from-code" || gotPayload["code_verifier"] != "verifier-value" || gotPayload["client_id"] == "" || gotPayload["redirect_uri"] == "" || gotPayload["expires_in"] != float64(31536000) {
		t.Fatalf("unexpected exchange payload: %#v", gotPayload)
	}
}

func TestAPIDebugLoggingCapturesRequestShapeWithoutLeakingSecrets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-debug","choices":[],"usage":{"total_tokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	const upstreamSecret = "sk-upstream-debug-secret"
	const promptSecret = "super-secret prompt from cline"
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, nil))

	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{
		ID:         "acct_debug",
		Type:       "openai_api_key",
		Label:      "Debug Account",
		Tier:       "simple",
		Credential: "api_key=" + upstreamSecret,
		BaseURL:    upstream.URL,
		Enabled:    true,
	}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.NewWithOptions(store, logger, server.Options{DebugAPI: true}).Handler())
	defer srv.Close()

	body := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"` + promptSecret + `"}],"stream":true,"metadata":{"source":"roo-cline"}}`)
	resp := doRequest(t, http.MethodPost, srv.URL+"/v1/chat/completions", map[string]string{
		"Authorization":  "Bearer " + store.GatewayKey(),
		"User-Agent":     "Cline/3.19.2",
		"OpenAI-Beta":    "assistants=v2",
		"X-Cline-Client": "roo-cline",
	}, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("debug request status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	logs := logBuffer.String()
	if !strings.Contains(logs, `"msg":"gateway_api_debug_request"`) {
		t.Fatalf("expected debug log event, got %s", logs)
	}
	if !strings.Contains(logs, `"msg":"gateway_api_debug_upstream_request"`) {
		t.Fatalf("expected upstream debug log event, got %s", logs)
	}
	for _, expected := range []string{
		`"path":"/v1/chat/completions"`,
		`"gateway_path":"/v1/chat/completions"`,
		`"upstream_path":"/v1/chat/completions"`,
		`"account_id":"acct_debug"`,
		`"upstream_body_sha256"`,
		`"authorization_scheme":"Bearer"`,
		`"json_valid":true`,
		`"parsed_messages_count":1`,
		`"json_top_level_keys":["messages","metadata","model","stream"]`,
		`"X-Cline-Client":"roo-cline"`,
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("expected log fragment %q in %s", expected, logs)
		}
	}
	for _, forbidden := range []string{store.GatewayKey(), upstreamSecret, promptSecret} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("debug log leaked secret %q in %s", forbidden, logs)
		}
	}
}

func assertStatus(t *testing.T, method string, url string, headers map[string]string, body []byte, want int) {
	t.Helper()
	resp := doRequest(t, method, url, headers, body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s status = %d, want %d", method, url, resp.StatusCode, want)
	}
}

func doRequest(t *testing.T, method string, url string, headers map[string]string, body []byte) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if body != nil && !strings.Contains(headers["Content-Type"], "application/json") {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if key == "X-Test-No-Redirect" {
			continue
		}
		req.Header.Set(key, value)
	}
	client := http.DefaultClient
	if headers["X-Test-No-Redirect"] != "" {
		client = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

func firstCookie(t *testing.T, resp *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range resp.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found", name)
	return nil
}

func loginCookie(t *testing.T, serverURL string, password string) *http.Cookie {
	t.Helper()
	loginResp := doRequest(t, http.MethodPost, serverURL+"/api/admin/login", nil, []byte(`{"password":"`+password+`"}`))
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", loginResp.StatusCode)
	}
	return firstCookie(t, loginResp, dashboard.CookieName)
}

type fakeTunnelController struct {
	mu       sync.Mutex
	status   tunnel.RuntimeStatus
	applies  []fakeTunnelApply
	stops    int
	applyErr error
}

type fakeTunnelApply struct {
	cfg  config.TunnelConfig
	bind string
}

func (f *fakeTunnelController) Apply(cfg config.TunnelConfig, bind string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies = append(f.applies, fakeTunnelApply{cfg: cfg, bind: bind})
	if f.applyErr != nil {
		f.status = tunnel.RuntimeStatus{Status: tunnel.StatusError, ErrorMessage: f.applyErr.Error()}
		return f.applyErr
	}
	if cfg.Enabled {
		f.status = tunnel.RuntimeStatus{Status: tunnel.StatusConnected, PublicURL: "https://fake.trycloudflare.com", RecentLogs: []string{"fake connected"}}
		return nil
	}
	f.status = tunnel.RuntimeStatus{Status: tunnel.StatusStopped}
	return nil
}

func (f *fakeTunnelController) Status() tunnel.RuntimeStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	status := f.status
	status.RecentLogs = append([]string(nil), f.status.RecentLogs...)
	return status
}

func (f *fakeTunnelController) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.status = tunnel.RuntimeStatus{Status: tunnel.StatusStopped}
	return nil
}

func writeFixtureJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode fixture JSON: %v", err)
	}
}
