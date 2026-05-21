package upstreamcompat_test

import (
	"strings"
	"testing"

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
