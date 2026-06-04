package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/upstreamcompat"
)

var jsonStringLiteralPattern = regexp.MustCompile(`"(?:\\.|[^"\\])*"`)
var whitespaceCollapsePattern = regexp.MustCompile(`\s+`)

func (h Handler) logAPIDebugRequest(r *http.Request, key config.GatewayKey, body []byte, parsed upstreamcompat.ChatCompletionRequest, parseErr error) {
	if !h.DebugAPI || h.Logger == nil || r == nil {
		return
	}
	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"query", r.URL.RawQuery,
		"content_length", r.ContentLength,
		"content_type", strings.TrimSpace(r.Header.Get("Content-Type")),
		"accept", strings.TrimSpace(r.Header.Get("Accept")),
		"user_agent", truncateDebugValue(r.UserAgent(), 240),
		"gateway_key_id", key.ID,
		"gateway_key_ref", key.Preview,
		"authorization_present", strings.TrimSpace(r.Header.Get("Authorization")) != "",
		"authorization_scheme", authorizationScheme(r.Header.Get("Authorization")),
		"header_keys", sortedHeaderKeys(r.Header),
		"headers", sanitizeDebugHeaders(r.Header),
		"body_bytes", len(body),
	}
	if len(body) > 0 {
		attrs = append(attrs, "body_sha256", sha256Hex(body))
	}
	if parseErr != nil {
		attrs = append(attrs, "parse_error", parseErr.Error())
	}
	attrs = append(attrs, summarizeParsedRequest(body, parsed)...)
	h.Logger.Info("gateway_api_debug_request", attrs...)
}

func (h Handler) logAPIDebugUpstreamRequest(base *http.Request, upstream *http.Request, account config.Account) {
	if !h.DebugAPI || h.Logger == nil || upstream == nil {
		return
	}
	body, bodyErr := readAndRestoreRequestBody(upstream)
	attrs := []any{
		"gateway_path", requestPathForDebug(base),
		"upstream_method", upstream.Method,
		"upstream_scheme", urlPartForDebug(upstream.URL, "scheme"),
		"upstream_host", urlPartForDebug(upstream.URL, "host"),
		"upstream_path", urlPartForDebug(upstream.URL, "path"),
		"upstream_query", sanitizeDebugQuery(queryValuesForDebug(upstream.URL)),
		"upstream_content_length", upstream.ContentLength,
		"upstream_header_keys", sortedHeaderKeys(upstream.Header),
		"upstream_headers", sanitizeDebugHeaders(upstream.Header),
		"account_id", account.ID,
		"account_type", strings.TrimSpace(account.Type),
		"account_platform", config.AccountPlatform(account),
		"account_tier", strings.TrimSpace(account.Tier),
		"upstream_body_bytes", len(body),
	}
	if bodyErr != nil {
		attrs = append(attrs, "upstream_body_read_error", bodyErr.Error())
	}
	if len(body) > 0 {
		attrs = append(attrs, "upstream_body_sha256", sha256Hex(body))
		parsed, _, parseErr := upstreamcompat.ParseChatCompletionRequest(bytes.NewReader(body), 4<<20)
		if parseErr != nil {
			attrs = append(attrs, "upstream_parse_error", parseErr.Error())
		}
		attrs = append(attrs, summarizeParsedRequest(body, parsed)...)
	}
	h.Logger.Info("gateway_api_debug_upstream_request", attrs...)
}

func readAndRestoreRequestBody(request *http.Request) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(request.Body)
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	return body, err
}

func requestPathForDebug(request *http.Request) string {
	if request == nil || request.URL == nil {
		return ""
	}
	return request.URL.Path
}

func urlPartForDebug(value *url.URL, part string) string {
	if value == nil {
		return ""
	}
	switch part {
	case "scheme":
		return value.Scheme
	case "host":
		return value.Host
	case "path":
		return value.Path
	default:
		return ""
	}
}

func queryValuesForDebug(value *url.URL) url.Values {
	if value == nil {
		return nil
	}
	return value.Query()
}

func sanitizeDebugQuery(values url.Values) map[string]any {
	out := make(map[string]any, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		items := values[key]
		if isSensitiveHeader(key) {
			out[key] = redactHeaderValues(key, items)
			continue
		}
		if len(items) == 1 {
			out[key] = truncateDebugValue(items[0], 240)
			continue
		}
		safeValues := make([]string, 0, len(items))
		for _, item := range items {
			safeValues = append(safeValues, truncateDebugValue(item, 240))
		}
		out[key] = safeValues
	}
	return out
}

func summarizeParsedRequest(body []byte, parsed upstreamcompat.ChatCompletionRequest) []any {
	attrs := []any{
		"parsed_model", strings.TrimSpace(parsed.Model),
		"parsed_stream", parsed.Stream,
		"parsed_messages_count", len(parsed.Messages),
		"parsed_input_present", len(parsed.Input) > 0,
		"parsed_metadata_keys", sortedMapKeys(parsed.Metadata),
	}
	if len(body) == 0 {
		return attrs
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return append(attrs,
			"json_valid", false,
			"body_preview", scrubBodyPreview(body),
		)
	}
	return append(attrs,
		"json_valid", true,
		"json_top_level_keys", topLevelJSONKeys(payload),
		"json_shape", summarizeJSONShape(payload, 0),
	)
}

func summarizeJSONShape(value any, depth int) any {
	if depth >= 6 {
		return "<max-depth>"
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(keys))
		for _, key := range keys {
			out[key] = summarizeJSONShape(typed[key], depth+1)
		}
		return out
	case []any:
		out := map[string]any{"$type": "array", "length": len(typed)}
		if len(typed) == 0 {
			return out
		}
		limit := len(typed)
		if limit > 3 {
			limit = 3
		}
		sample := make([]any, 0, limit)
		for i := 0; i < limit; i++ {
			sample = append(sample, summarizeJSONShape(typed[i], depth+1))
		}
		out["items"] = sample
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return "<string:empty>"
		}
		return "<string:" + strconv.Itoa(len(typed)) + ">"
	case bool:
		return typed
	case nil:
		return nil
	case float64:
		return "<number>"
	default:
		return "<value>"
	}
}

func sanitizeDebugHeaders(header http.Header) map[string]any {
	keys := sortedHeaderKeys(header)
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		values := header.Values(key)
		if len(values) == 0 {
			continue
		}
		if isSensitiveHeader(key) {
			out[key] = redactHeaderValues(key, values)
			continue
		}
		if len(values) == 1 {
			out[key] = truncateDebugValue(values[0], 240)
			continue
		}
		safeValues := make([]string, 0, len(values))
		for _, value := range values {
			safeValues = append(safeValues, truncateDebugValue(value, 240))
		}
		out[key] = safeValues
	}
	return out
}

func redactHeaderValues(key string, values []string) any {
	if strings.EqualFold(key, "Authorization") {
		scheme := authorizationScheme(firstHeaderValue(values))
		if scheme == "" {
			scheme = "present"
		}
		return scheme + " <redacted>"
	}
	if len(values) == 1 {
		return "<redacted>"
	}
	redacted := make([]string, 0, len(values))
	for range values {
		redacted = append(redacted, "<redacted>")
	}
	return redacted
}

func authorizationScheme(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.Fields(header)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func sortedHeaderKeys(header http.Header) []string {
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, http.CanonicalHeaderKey(key))
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func topLevelJSONKeys(payload any) []string {
	root, ok := payload.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func scrubBodyPreview(body []byte) string {
	preview := string(body)
	if len(preview) > 512 {
		preview = preview[:512] + "...(truncated)"
	}
	preview = jsonStringLiteralPattern.ReplaceAllString(preview, `"<string>"`)
	preview = whitespaceCollapsePattern.ReplaceAllString(strings.TrimSpace(preview), " ")
	return preview
}

func truncateDebugValue(value string, max int) string {
	trimmed := strings.TrimSpace(value)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	if max <= 16 {
		return trimmed[:max]
	}
	return trimmed[:max-16] + "...(truncated)"
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func isSensitiveHeader(key string) bool {
	canonical := strings.ToLower(strings.TrimSpace(key))
	if canonical == "authorization" || canonical == "proxy-authorization" || canonical == "cookie" || canonical == "set-cookie" || canonical == "x-api-key" || canonical == "api-key" {
		return true
	}
	return strings.Contains(canonical, "token") || strings.Contains(canonical, "secret") || strings.Contains(canonical, "password")
}

func firstHeaderValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
