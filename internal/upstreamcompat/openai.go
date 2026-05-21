package upstreamcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type ChatCompletionRequest struct {
	Model    string            `json:"model"`
	Messages []json.RawMessage `json:"messages"`
	Stream   bool              `json:"stream,omitempty"`
	Metadata map[string]any    `json:"metadata,omitempty"`
}

type OpenAIErrorEnvelope struct {
	Error OpenAIError `json:"error"`
}

type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

func ParseChatCompletionRequest(r io.Reader, maxBytes int64) (ChatCompletionRequest, []byte, error) {
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return ChatCompletionRequest{}, nil, err
	}
	if int64(len(body)) > maxBytes {
		return ChatCompletionRequest{}, nil, errors.New("request body too large")
	}
	var req ChatCompletionRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&req); err != nil {
		return ChatCompletionRequest{}, nil, fmt.Errorf("invalid JSON request: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ChatCompletionRequest{}, nil, errors.New("request must contain a single JSON object")
	}
	if strings.TrimSpace(req.Model) == "" {
		return ChatCompletionRequest{}, nil, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return ChatCompletionRequest{}, nil, errors.New("messages are required")
	}
	return req, body, nil
}

func ErrorEnvelope(message string, typ string, code string) OpenAIErrorEnvelope {
	if typ == "" {
		typ = "invalid_request_error"
	}
	return OpenAIErrorEnvelope{Error: OpenAIError{Message: message, Type: typ, Code: code}}
}

func IsOpenAIErrorStatus(status int) bool {
	return status == 401 || status == 403 || status == 404 || status == 408 || status == 409 || status == 429 || status >= 500
}

func ErrorTypeForStatus(status int) string {
	switch status {
	case 400:
		return "invalid_request_error"
	case 401, 403:
		return "authentication_error"
	case 404:
		return "not_found_error"
	case 429:
		return "rate_limit_error"
	default:
		if status >= 500 {
			return "server_error"
		}
		return "api_error"
	}
}
