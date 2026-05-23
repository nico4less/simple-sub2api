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

func gatewayServerWithOptions(t *testing.T, accounts []config.Account, policy config.GroupRotationPolicy, addGroupTag bool) (*httptest.Server, *config.Store) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Dashboard.AdminPassword = "admin-secret"
	cfg.Accounts = accounts
	cfg.Quota.Policies = []config.QuotaPolicy{{ID: "daily", DailyLimitTokens: 1000, Source: "local"}}
	accountIDs := make([]string, 0, len(cfg.Accounts))
	for i := range cfg.Accounts {
		cfg.Accounts[i].QuotaPolicy = "daily"
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
