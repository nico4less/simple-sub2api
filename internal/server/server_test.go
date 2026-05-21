package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/dashboard"
	"github.com/0xForce-Network/simple-sub2api/internal/server"
)

func TestM0SecurityBoundaries(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	assertStatus(t, http.MethodGet, srv.URL+"/healthz", nil, nil, http.StatusOK)
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", nil, nil, http.StatusUnauthorized)
	assertStatus(t, http.MethodGet, srv.URL+"/v1/models", map[string]string{"Authorization": "Bearer " + store.GatewayKey()}, nil, http.StatusOK)
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

func TestQueueEDashboardShellAndStateAreAdminProtected(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = []config.Account{{ID: "acct_1", Type: "openai_api_key", Label: "Simple", Tier: "simple", Credential: "api_key=sk-local", Enabled: true}}
	store, err := config.NewMemoryStore(cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	srv := httptest.NewServer(server.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	shellResp := doRequest(t, http.MethodGet, srv.URL+"/dashboard", nil, nil)
	if shellResp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard shell status = %d", shellResp.StatusCode)
	}
	shellBody, err := io.ReadAll(shellResp.Body)
	if err != nil {
		t.Fatalf("read dashboard shell: %v", err)
	}
	if !strings.Contains(string(shellBody), "Simple Sub2API Dashboard") || strings.Contains(string(shellBody), "https://") {
		t.Fatalf("dashboard shell missing title or contains external asset reference")
	}
	for _, marker := range []string{"Account Add / Save", "OAuth Sources", "Subscription Import Preview / Apply", "Routing Edit / Test", "Proxy Config / Probe", "QPS", "recentErrors", "loadMetrics()", "quotaBars", "applyImport()", "refreshOAuth", "reauthOAuth"} {
		if !strings.Contains(string(shellBody), marker) {
			t.Fatalf("dashboard shell missing marker %q", marker)
		}
	}

	assertStatus(t, http.MethodGet, srv.URL+"/api/admin/dashboard/state", nil, nil, http.StatusUnauthorized)
	cookie := loginCookie(t, srv.URL, "admin-secret")
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
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
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
