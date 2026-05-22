package upstreamcompat_test

import (
	"io"
	"net/http"
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
		t.Fatal("ParseChatCompletionRequest() accepted missing messages")
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
