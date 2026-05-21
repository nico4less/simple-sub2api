package subscriptionimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/accountcompat"
	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

const maxImportBytes = 1 << 20

type Request struct {
	Kind        string   `json:"kind"`
	URL         string   `json:"url,omitempty"`
	Content     string   `json:"content,omitempty"`
	Label       string   `json:"label,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Platform    string   `json:"platform,omitempty"`
	AccountType string   `json:"account_type,omitempty"`
}

type Preview struct {
	Sources      []config.SubscriptionSource `json:"sources"`
	Accounts     []config.Account            `json:"accounts"`
	Proxies      []config.ProxyConfig        `json:"proxies"`
	AccountData  *accountcompat.DataPayload  `json:"account_data,omitempty"`
	ImportResult accountcompat.ImportResult  `json:"import_result"`
	Duplicates   []string                    `json:"duplicates"`
	Conflicts    []string                    `json:"conflicts"`
	Warnings     []string                    `json:"warnings"`
	Redacted     bool                        `json:"redacted"`
	SourceImpact SourceImpact                `json:"source_impact,omitempty"`
}

type SourceImpact struct {
	SourceID        string   `json:"source_id"`
	DerivedAccounts []string `json:"derived_accounts"`
}

func PreviewImport(ctx context.Context, req Request, existing config.Config) (Preview, error) {
	return PrepareImport(ctx, req, existing, true)
}

func PrepareImport(ctx context.Context, req Request, existing config.Config, redacted bool) (Preview, error) {
	content := req.Content
	if req.Kind == "url" {
		fetched, err := fetchURL(ctx, req.URL)
		if err != nil {
			return Preview{}, err
		}
		content = fetched
	}
	if strings.TrimSpace(content) == "" {
		return Preview{}, errors.New("import content is required")
	}

	var preview Preview
	switch req.Kind {
	case "line_tokens", "url":
		payload := accountcompat.PayloadFromLineTokens(content, defaultString(req.Platform, accountcompat.PlatformOpenAI), defaultString(req.AccountType, accountcompat.AccountTypeAPIKey))
		parsed, err := previewFromPayload(payload, req, existing, redacted)
		if err != nil {
			return Preview{}, err
		}
		preview = parsed
	case "json_bundle", "inline_bundle":
		parsed, err := parseJSONBundle(content, req, existing, redacted)
		if err != nil {
			return Preview{}, err
		}
		preview = parsed
	default:
		return Preview{}, errors.New("import kind must be line_tokens, json_bundle, inline_bundle, or url")
	}

	preview.Redacted = redacted
	annotateDiff(&preview, existing, redacted)
	return preview, nil
}

func ApplyPreview(cfg *config.Config, preview Preview) {
	cfg.SubscriptionSources = mergeSources(cfg.SubscriptionSources, preview.Sources)
	cfg.Proxies = mergeProxies(cfg.Proxies, preview.Proxies)
	cfg.Accounts = mergeAccounts(cfg.Accounts, preview.Accounts)
}

func ImpactForSource(sourceID string, cfg config.Config) SourceImpact {
	impact := SourceImpact{SourceID: sourceID}
	for _, account := range cfg.Accounts {
		if account.SourceID == sourceID {
			impact.DerivedAccounts = append(impact.DerivedAccounts, account.ID)
		}
	}
	sort.Strings(impact.DerivedAccounts)
	return impact
}

func parseJSONBundle(content string, req Request, existing config.Config, redacted bool) (Preview, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var payload accountcompat.DataPayload
	if err := decoder.Decode(&payload); err != nil {
		return Preview{}, fmt.Errorf("invalid JSON bundle: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Preview{}, errors.New("JSON bundle must contain a single object")
	}
	return previewFromPayload(payload, req, existing, redacted)
}

func previewFromPayload(payload accountcompat.DataPayload, req Request, existing config.Config, redacted bool) (Preview, error) {
	prepared, err := accountcompat.PreparePayload(payload, existing, defaultString(req.Label, "sub2api Account Management import"), defaultString(req.Tier, "simple"), req.Tags, redacted)
	if err != nil {
		return Preview{}, err
	}
	accountData := prepared.Payload
	if redacted {
		prepared = accountcompat.MaskPrepared(prepared)
		accountData = prepared.Payload
	}
	return Preview{Sources: prepared.Sources, Accounts: prepared.Accounts, Proxies: prepared.Proxies, AccountData: &accountData, ImportResult: prepared.Result, Redacted: redacted}, nil
}

func annotateDiff(preview *Preview, existing config.Config, redacted bool) {
	existingAccounts := map[string]bool{}
	for _, account := range existing.Accounts {
		existingAccounts[account.ID] = true
	}
	for _, account := range preview.Accounts {
		if existingAccounts[account.ID] {
			preview.Conflicts = append(preview.Conflicts, "account:"+account.ID)
		}
	}
	existingSources := map[string]bool{}
	for _, source := range existing.SubscriptionSources {
		existingSources[source.ID] = true
	}
	for _, source := range preview.Sources {
		if existingSources[source.ID] {
			preview.Conflicts = append(preview.Conflicts, "source:"+source.ID)
		}
	}
	if redacted {
		redactPreview(preview)
	}
}

func redactPreview(preview *Preview) {
	for i := range preview.Accounts {
		preview.Accounts[i].Credential = maskSecret(preview.Accounts[i].Credential)
	}
	for i := range preview.Sources {
		preview.Sources[i].InlineBundle = maskSecret(preview.Sources[i].InlineBundle)
	}
}

func fetchURL(ctx context.Context, raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("subscription URL must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("subscription URL scheme must be http or https")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("subscription URL returned status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxImportBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(data) > maxImportBytes {
		return "", errors.New("subscription content exceeds 1 MiB limit")
	}
	return string(data), nil
}

func mergeSources(existing []config.SubscriptionSource, incoming []config.SubscriptionSource) []config.SubscriptionSource {
	index := map[string]int{}
	out := append([]config.SubscriptionSource(nil), existing...)
	for i, source := range out {
		index[source.ID] = i
	}
	for _, source := range incoming {
		if i, ok := index[source.ID]; ok {
			out[i] = source
			continue
		}
		out = append(out, source)
	}
	return out
}

func mergeProxies(existing []config.ProxyConfig, incoming []config.ProxyConfig) []config.ProxyConfig {
	index := map[string]int{}
	out := append([]config.ProxyConfig(nil), existing...)
	for i, proxy := range out {
		index[proxy.ID] = i
	}
	for _, proxy := range incoming {
		if i, ok := index[proxy.ID]; ok {
			out[i] = proxy
			continue
		}
		out = append(out, proxy)
	}
	return out
}

func mergeAccounts(existing []config.Account, incoming []config.Account) []config.Account {
	index := map[string]int{}
	out := append([]config.Account(nil), existing...)
	for i, account := range out {
		index[account.ID] = i
	}
	for _, account := range incoming {
		if i, ok := index[account.ID]; ok {
			out[i] = account
			continue
		}
		out = append(out, account)
	}
	return out
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func maskSecret(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "redacted" {
		return trimmed
	}
	if len(trimmed) <= 8 {
		return "redacted"
	}
	var b bytes.Buffer
	b.WriteString(trimmed[:4])
	b.WriteString("...")
	b.WriteString(trimmed[len(trimmed)-4:])
	return b.String()
}
