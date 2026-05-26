package upstreamcompat_test

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/upstreamcompat"
)

func TestParseChatCompletionRequestStrictContract(t *testing.T) {
	req, body, err := upstreamcompat.ParseChatCompletionRequest(strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"stream":true}`), 1024)
	if err != nil {
		t.Fatalf("ParseChatCompletionRequest() error = %v", err)
	}
	if req.Model != "gpt-test" || !req.Stream || len(req.Messages) != 1 || len(body) == 0 {
		t.Fatalf("parsed request = %#v body=%q", req, string(body))
	}
	if _, _, err := upstreamcompat.ParseChatCompletionRequest(strings.NewReader(`{"model":"gpt-test"}`), 1024); err == nil {
		t.Fatal("ParseChatCompletionRequest() accepted missing messages/input")
	}
}

func TestParseChatCompletionRequestAcceptsResponsesInput(t *testing.T) {
	req, body, err := upstreamcompat.ParseChatCompletionRequest(strings.NewReader(`{"model":"gpt-test","input":"who are you"}`), 1024)
	if err != nil {
		t.Fatalf("ParseChatCompletionRequest() error = %v", err)
	}
	if req.Model != "gpt-test" || len(req.Input) == 0 || len(req.Messages) != 0 || len(body) == 0 {
		t.Fatalf("parsed request = %#v body=%q", req, string(body))
	}
}

func TestOpenAIErrorEnvelope(t *testing.T) {
	envelope := upstreamcompat.ErrorEnvelope("bad", "invalid_request_error", "invalid_request")
	if envelope.Error.Message != "bad" || envelope.Error.Code != "invalid_request" {
		t.Fatalf("envelope = %#v", envelope)
	}
	if got := upstreamcompat.ErrorTypeForStatus(429); got != "rate_limit_error" {
		t.Fatalf("ErrorTypeForStatus(429) = %q", got)
	}
}

func TestOpenAIAPIURLNormalizesOriginAndV1Base(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		endpoint string
		want     string
	}{
		{name: "origin models", baseURL: "https://api.openai.com", endpoint: "/v1/models", want: "https://api.openai.com/v1/models"},
		{name: "v1 models", baseURL: "https://api.openai.com/v1", endpoint: "/v1/models", want: "https://api.openai.com/v1/models"},
		{name: "origin chat", baseURL: "https://api.openai.com", endpoint: "/v1/chat/completions", want: "https://api.openai.com/v1/chat/completions"},
		{name: "v1 chat", baseURL: "https://api.openai.com/v1", endpoint: "/v1/chat/completions", want: "https://api.openai.com/v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := upstreamcompat.OpenAIAPIURL(tt.baseURL, tt.endpoint)
			if err != nil {
				t.Fatalf("OpenAIAPIURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("OpenAIAPIURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildUpstreamRequestNormalizesV1Base(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{BaseURL: "https://api.openai.com/v1", Credential: "api_key=sk-upstream"}, []byte(`{"model":"gpt-test"}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	if got, want := request.URL.String(), "https://api.openai.com/v1/chat/completions"; got != want {
		t.Fatalf("upstream URL = %q, want %q", got, want)
	}
}

func TestBuildUpstreamRequestPreservesResponsesEndpoint(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{BaseURL: "https://api.openai.com/v1", Credential: "api_key=sk-upstream"}, []byte(`{"model":"gpt-test","input":"hi"}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	if got, want := request.URL.String(), "https://api.openai.com/v1/responses"; got != want {
		t.Fatalf("upstream URL = %q, want %q", got, want)
	}
}

func TestBuildUpstreamRequestUsesCodexInternalForRooOpenAIOAuth(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	base.Header.Set("Accept", "*/*")
	base.Header.Set("User-Agent", "roo-code/3.53.0")
	base.Header.Set("Originator", "roo-code")
	base.Header.Set("X-Stainless-Lang", "js")
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{Type: "oauth", BaseURL: "https://api.openai.com/v1", Credential: "access_token=oauth-access;chatgpt_account_id=chatgpt-acc", Metadata: map[string]any{"platform": "openai"}}, []byte(`{"model":"gpt-test","input":"hi","stream":true,"max_output_tokens":4096}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	if got, want := request.URL.String(), "https://chatgpt.com/backend-api/codex/responses"; got != want {
		t.Fatalf("upstream URL = %q, want %q", got, want)
	}
	if got := request.Host; got != "chatgpt.com" {
		t.Fatalf("Host = %q, want chatgpt.com", got)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer oauth-access" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := request.Header.Get("User-Agent"); got != "codex_cli_rs/0.125.0" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := request.Header.Get("Originator"); got != "codex_cli_rs" {
		t.Fatalf("Originator = %q", got)
	}
	if got := request.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	if got := request.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
	gotBody, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if strings.Contains(string(gotBody), `"max_output_tokens"`) {
		t.Fatalf("codex internal body should strip max_output_tokens: %s", string(gotBody))
	}
}

func TestBuildUpstreamRequestCanForceCodexInternalForRooOpenAIOAuth(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	base.Header.Set("Accept", "*/*")
	base.Header.Set("User-Agent", "roo-code/3.53.0")
	base.Header.Set("Originator", "roo-code")
	base.Header.Set("X-Stainless-Lang", "js")
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{Type: "oauth", BaseURL: "https://api.openai.com/v1", Credential: "access_token=oauth-access;force_codex_responses=true", Metadata: map[string]any{"platform": "openai"}}, []byte(`{"model":"gpt-test","input":"hi","stream":true,"max_output_tokens":4096}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	if got, want := request.URL.String(), "https://chatgpt.com/backend-api/codex/responses"; got != want {
		t.Fatalf("upstream URL = %q, want %q", got, want)
	}
	if got := request.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
	gotBody, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if strings.Contains(string(gotBody), `"max_output_tokens"`) {
		t.Fatalf("forced codex body still contains unsupported max_output_tokens: %s", string(gotBody))
	}
}

func TestBuildUpstreamRequestTreatsCodexOriginatorAsInternalClient(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	base.Header.Set("Accept", "*/*")
	base.Header.Set("User-Agent", "openai-node/4.0.0")
	base.Header.Set("Originator", "codex_cli_rs")
	base.Header.Set("X-Stainless-Lang", "js")
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{Type: "oauth", BaseURL: "https://api.openai.com/v1", Credential: "access_token=oauth-access", Metadata: map[string]any{"platform": "openai"}}, []byte(`{"model":"gpt-test","input":"hi","stream":true}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	if got, want := request.URL.String(), "https://chatgpt.com/backend-api/codex/responses"; got != want {
		t.Fatalf("upstream URL = %q, want %q", got, want)
	}
}

func TestBuildUpstreamRequestNormalizesCodexInternalBodyLikeUpstream(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	body := []byte(`{"model":"gpt-test","input":"hi","stream":false,"store":true,"max_output_tokens":4096,"max_completion_tokens":4096,"temperature":0.2,"top_p":0.9,"frequency_penalty":1,"presence_penalty":1,"user":"u","metadata":{"user_id":"u"},"prompt_cache_retention":"24h","safety_identifier":"sid","stream_options":{"include_usage":true}}`)
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{Type: "oauth", BaseURL: "https://api.openai.com/v1", Credential: "access_token=oauth-access", Metadata: map[string]any{"platform": "openai"}}, body)
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	gotBody, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "temperature", "top_p", "frequency_penalty", "presence_penalty", "user", "metadata", "prompt_cache_retention", "safety_identifier", "stream_options"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("%s should be stripped for Codex internal path: %s", key, string(gotBody))
		}
	}
	if got := payload["store"]; got != false {
		t.Fatalf("store = %#v", got)
	}
	if got := payload["stream"]; got != true {
		t.Fatalf("stream = %#v", got)
	}
}

func TestBuildUpstreamRequestUsesChatGPTCodexForOpenAIOAuth(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	base.Header.Set("Authorization", "Bearer local-gateway-key")
	base.Header.Set("User-Agent", "OpenAI/Python 1.2.3")
	base.Header.Set("session_id", "client-session")
	base.Header.Set("conversation_id", "client-conversation")
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{Type: "oauth", BaseURL: "https://api.openai.com/v1", Credential: "access_token=oauth-access;chatgpt_account_id=chatgpt-acc", Metadata: map[string]any{"platform": "openai"}}, []byte(`{"model":"gpt-test","input":"hi","prompt_cache_key":"cache-key"}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	if got, want := request.URL.String(), "https://chatgpt.com/backend-api/codex/responses"; got != want {
		t.Fatalf("upstream URL = %q, want %q", got, want)
	}
	if got := request.Host; got != "chatgpt.com" {
		t.Fatalf("Host = %q", got)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer oauth-access" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := request.Header.Get("chatgpt-account-id"); got != "chatgpt-acc" {
		t.Fatalf("chatgpt-account-id = %q", got)
	}
	if got := request.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	if got := request.Header.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q", got)
	}
	if got := request.Header.Get("User-Agent"); got != "codex_cli_rs/0.125.0" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := request.Header.Get("session_id"); got == "" || got == "client-session" {
		t.Fatalf("session_id was not isolated: %q", got)
	}
	if got := request.Header.Get("conversation_id"); got == "" || got == "client-conversation" {
		t.Fatalf("conversation_id was not isolated: %q", got)
	}
}

func TestBuildUpstreamRequestUsesStreamingAcceptForOpenAIOAuthResponses(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	base.Header.Set("Accept", "*/*")
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{Type: "oauth", BaseURL: "https://api.openai.com/v1", Credential: "access_token=oauth-access", Metadata: map[string]any{"platform": "openai"}}, []byte(`{"model":"gpt-test","input":"hi","stream":true}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	if got := request.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
}

func TestBuildUpstreamRequestStripsUnsupportedMaxOutputTokensForOpenAIOAuthResponses(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/responses", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	body := []byte(`{"model":"gpt-test","input":"hi","stream":true,"max_output_tokens":4096}`)
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{Type: "oauth", BaseURL: "https://api.openai.com/v1", Credential: "access_token=oauth-access", Metadata: map[string]any{"platform": "openai"}}, body)
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	gotBody, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, ok := payload["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens should be stripped: %s", string(gotBody))
	}
	if got := payload["stream"]; got != true {
		t.Fatalf("stream = %#v", got)
	}
}

func TestBuildUpstreamRequestPreservesProtocolHeadersAndRawBody(t *testing.T) {
	body := []byte("{\n  \"model\" : \"gpt-test\",\n  \"messages\" : [{\"role\":\"user\",\"content\":\"hi\"}]\n}\n")
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	base.Header.Set("Authorization", "Bearer local-gateway-key")
	base.Header.Set("User-Agent", "OpenAI/Python 1.2.3")
	base.Header.Set("Accept", "text/event-stream")
	base.Header.Set("Content-Type", "application/json; charset=utf-8")
	base.Header.Set("Anthropic-Version", "2023-06-01")
	base.Header.Set("OpenAI-Beta", "assistants=v2")
	base.Header.Set("X-Simple-Sub2API-Debug", "local-only")

	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{BaseURL: "https://api.openai.com", Credential: "api_key=sk-upstream"}, body)
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	gotBody, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("upstream body = %q, want raw %q", string(gotBody), string(body))
	}
	if got := request.Header.Get("Authorization"); got != "Bearer sk-upstream" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := request.Header.Get("User-Agent"); got != "OpenAI/Python 1.2.3" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := request.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
	if got := request.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := request.Header.Get("Anthropic-Version"); got != "2023-06-01" {
		t.Fatalf("Anthropic-Version = %q", got)
	}
	if got := request.Header.Get("OpenAI-Beta"); got != "assistants=v2" {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	if got := request.Header.Get("X-Simple-Sub2API-Debug"); got != "" {
		t.Fatalf("local debug header leaked upstream: %q", got)
	}
}

func TestBuildUpstreamRequestSetsDefaultUserAgent(t *testing.T) {
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{BaseURL: "https://api.openai.com", Credential: "sk-upstream"}, []byte(`{"model":"gpt-test"}`))
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	if got := request.Header.Get("User-Agent"); got != "simple-sub2api-gateway/1.0" {
		t.Fatalf("default User-Agent = %q", got)
	}
	if got := request.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("default Accept = %q", got)
	}
}

func TestBuildUpstreamRequestSignsClaudeCodeBillingHeader(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81.df2; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":[{"type":"text","text":"keep literal cch=00000 in user content"}]}]}`)
	base, err := http.NewRequest(http.MethodPost, "http://localhost/v1/chat/completions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	base.Header.Set("User-Agent", "claude-cli/2.1.22 (external, cli)")

	request, err := upstreamcompat.BuildUpstreamRequest(base, config.Account{BaseURL: "https://api.anthropic.com", Credential: "api_key=sk-upstream"}, body)
	if err != nil {
		t.Fatalf("BuildUpstreamRequest() error = %v", err)
	}
	gotBody, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	got := string(gotBody)
	if strings.Contains(got, "cc_version=2.1.81") || !strings.Contains(got, "cc_version=2.1.22.df2") {
		t.Fatalf("billing cc_version was not synced: %s", got)
	}
	if strings.Contains(got, "x-anthropic-billing-header: cc_version=2.1.22.df2; cc_entrypoint=cli; cch=00000;") {
		t.Fatalf("billing cch placeholder was not signed: %s", got)
	}
	if !regexp.MustCompile(`x-anthropic-billing-header: cc_version=2\.1\.22\.df2; cc_entrypoint=cli; cch=[0-9a-f]{5};`).MatchString(got) {
		t.Fatalf("billing cch signature missing or malformed: %s", got)
	}
	if !strings.Contains(got, "keep literal cch=00000 in user content") {
		t.Fatalf("user cch literal was modified: %s", got)
	}
}
