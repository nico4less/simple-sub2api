package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/accountcheck"
	"github.com/0xForce-Network/simple-sub2api/internal/accountpool"
	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/dashboard"
	"github.com/0xForce-Network/simple-sub2api/internal/gateway"
	"github.com/0xForce-Network/simple-sub2api/internal/gatewayauth"
	"github.com/0xForce-Network/simple-sub2api/internal/metrics"
	"github.com/0xForce-Network/simple-sub2api/internal/proxyclient"
	"github.com/0xForce-Network/simple-sub2api/internal/routing"
	subscriptionimport "github.com/0xForce-Network/simple-sub2api/internal/subscription_import"
	"github.com/0xForce-Network/simple-sub2api/internal/tunnel"
	"github.com/0xForce-Network/simple-sub2api/internal/version"
)

type Server struct {
	store          *config.Store
	logger         *slog.Logger
	sessions       *dashboard.SessionManager
	pool           *accountpool.Manager
	metrics        *metrics.Recorder
	tunnel         tunnel.Controller
	debugDashboard bool
}

const (
	openAIOAuthClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIOAuthRedirectURI = "http://localhost:1455/auth/callback"
	openAIOAuthTokenURL    = "https://auth.openai.com/oauth/token"
)

type Options struct {
	DebugDashboard bool
	TunnelManager  tunnel.Controller
}

func New(store *config.Store, logger *slog.Logger) *Server {
	return NewWithOptions(store, logger, Options{})
}

func NewWithOptions(store *config.Store, logger *slog.Logger, options Options) *Server {
	cfg := store.Snapshot()
	pool, err := accountpool.NewManager(cfg)
	if err != nil && logger != nil {
		logger.Error("account pool init failed", "error", err)
	}
	tunnelManager := options.TunnelManager
	if tunnelManager == nil {
		tunnelManager = tunnel.NewManager()
	}
	s := &Server{
		store:          store,
		logger:         logger,
		sessions:       dashboard.NewSessionManager(time.Duration(cfg.Dashboard.SessionTTLSeconds) * time.Second),
		pool:           pool,
		metrics:        metrics.NewRecorder(cfg.Metrics.RecentErrorsLimit),
		tunnel:         tunnelManager,
		debugDashboard: options.DebugDashboard,
	}
	if err := s.applyTunnelConfig(cfg); err != nil && logger != nil {
		logger.Error("tunnel init failed", "error", err)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/version", s.version)
	mux.HandleFunc("/assets/", s.dashboardAsset)
	mux.HandleFunc("/", s.dashboardIndex)
	mux.HandleFunc("/login", s.dashboardIndex)
	mux.HandleFunc("/dashboard", s.dashboardIndex)
	mux.HandleFunc("/keys", s.dashboardIndex)
	mux.HandleFunc("/admin/groups", s.dashboardIndex)
	mux.HandleFunc("/admin/accounts", s.dashboardIndex)
	mux.HandleFunc("/admin/proxies", s.dashboardIndex)
	mux.Handle("/v1/", gatewayauth.Authorizer{Provider: s.store}.Middleware(http.HandlerFunc(s.v1Gateway)))
	mux.HandleFunc("/api/admin/login", s.login)
	mux.HandleFunc("/api/admin/logout", s.logout)
	mux.Handle("/api/admin/me", s.adminOnly(http.HandlerFunc(s.me)))
	mux.Handle("/api/admin/dashboard/state", s.adminOnly(http.HandlerFunc(s.dashboardState)))
	mux.Handle("/api/admin/dashboard/recent-usage", s.adminOnly(http.HandlerFunc(s.recentUsage)))
	mux.Handle("/api/admin/gateway-key", s.adminOnly(http.HandlerFunc(s.revealGatewayKey)))
	mux.Handle("/api/admin/gateway-key/rotate", s.adminOnly(http.HandlerFunc(s.rotateGatewayKey)))
	mux.Handle("/api/keys", s.adminOnly(http.HandlerFunc(s.keys)))
	mux.Handle("/api/keys/", s.adminOnly(http.HandlerFunc(s.keyByID)))
	mux.Handle("/api/admin/config", s.adminOnly(http.HandlerFunc(s.configState)))
	mux.Handle("/api/admin/config/save", s.adminOnly(http.HandlerFunc(s.configSave)))
	mux.Handle("/api/admin/groups", s.adminOnly(http.HandlerFunc(s.groups)))
	mux.Handle("/api/admin/groups/", s.adminOnly(http.HandlerFunc(s.groupByID)))
	mux.Handle("/api/admin/accounts", s.adminOnly(http.HandlerFunc(s.accounts)))
	mux.Handle("/api/admin/accounts/import/preview", s.adminOnly(http.HandlerFunc(s.importPreview)))
	mux.Handle("/api/admin/accounts/import/apply", s.adminOnly(http.HandlerFunc(s.importApply)))
	mux.Handle("/api/admin/accounts/export", s.adminOnly(http.HandlerFunc(s.accountsExport)))
	mux.Handle("/api/admin/accounts/", s.adminOnly(http.HandlerFunc(s.accountByID)))
	mux.Handle("/api/admin/oauth-sources", s.adminOnly(http.HandlerFunc(s.oauthSources)))
	mux.Handle("/api/admin/oauth-sources/", s.adminOnly(http.HandlerFunc(s.oauthSourceByID)))
	mux.Handle("/api/admin/subscription-sources", s.adminOnly(http.HandlerFunc(s.subscriptionSources)))
	mux.Handle("/api/admin/subscription-sources/", s.adminOnly(http.HandlerFunc(s.subscriptionSourceByID)))
	mux.Handle("/api/admin/import/preview", s.adminOnly(http.HandlerFunc(s.importPreview)))
	mux.Handle("/api/admin/import/apply", s.adminOnly(http.HandlerFunc(s.importApply)))
	mux.Handle("/api/admin/proxies", s.adminOnly(http.HandlerFunc(s.proxies)))
	mux.Handle("/api/admin/proxies/", s.adminOnly(http.HandlerFunc(s.proxyByID)))
	mux.Handle("/api/admin/tunnel/status", s.adminOnly(http.HandlerFunc(s.tunnelStatus)))
	mux.Handle("/api/admin/tunnel/config", s.adminOnly(http.HandlerFunc(s.tunnelConfig)))
	mux.Handle("/api/admin/quota", s.adminOnly(http.HandlerFunc(s.quotaConfig)))
	mux.Handle("/api/admin/quota/state", s.adminOnly(http.HandlerFunc(s.quotaState)))
	mux.Handle("/api/admin/routing", s.adminOnly(http.HandlerFunc(s.routingConfig)))
	mux.Handle("/api/admin/routing/decide", s.adminOnly(http.HandlerFunc(s.routingDecide)))
	mux.Handle("/api/admin/account-check", s.adminOnly(http.HandlerFunc(s.accountCheck)))
	mux.Handle("/api/admin/account-pool", s.adminOnly(http.HandlerFunc(s.accountPool)))
	mux.Handle("/api/admin/metrics", s.adminOnly(http.HandlerFunc(s.metricsSnapshot)))
	mux.Handle("/api/admin/metrics/recent-usage", s.adminOnly(http.HandlerFunc(s.recentUsage)))
	mux.Handle("/api/admin/openai/oauth/exchange-code", s.adminOnly(http.HandlerFunc(s.openAIOAuthExchangeCode)))
	mux.Handle("/api/admin/debug/snapshot", s.adminOnly(http.HandlerFunc(s.debugSnapshot)))
	return s.cors(mux)
}

func (s *Server) StopTunnel() error {
	if s.tunnel == nil {
		return nil
	}
	return s.tunnel.Stop()
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
	})
}

func (s *Server) dashboardIndex(w http.ResponseWriter, r *http.Request) {
	if !isDashboardRoute(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authenticated := s.sessions.Authenticate(r)
	if r.URL.Path == "/" {
		redirectTarget := "/login"
		if authenticated {
			redirectTarget = "/dashboard"
		}
		http.Redirect(w, r, redirectTarget, http.StatusFound)
		return
	}
	if r.URL.Path == "/login" && authenticated {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	if r.URL.Path != "/login" && !authenticated {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	s.serveDashboardIndex(w, r)
}

func (s *Server) serveDashboardIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(dashboard.StaticFS, "static/index.html")
	if err != nil {
		http.Error(w, "dashboard asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (s *Server) dashboardAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	assetPath := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if !strings.HasPrefix(assetPath, "assets/") || strings.Contains(assetPath, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(dashboard.StaticFS, "static/"+assetPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(assetPath)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func isDashboardRoute(path string) bool {
	switch path {
	case "/", "/login", "/dashboard", "/keys", "/admin/groups", "/admin/accounts", "/admin/proxies":
		return true
	default:
		return false
	}
}

func (s *Server) v1Gateway(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/models" {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []any{}})
		return
	}
	gateway.Handler{Store: s.store, Pool: s.pool, Metrics: s.metrics, Logger: s.logger}.ServeHTTP(w, r)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid login request", http.StatusBadRequest)
		return
	}
	if !s.matchesAdminPassword(req.Password) {
		http.Error(w, "admin password required", http.StatusUnauthorized)
		return
	}
	if err := s.sessions.Issue(w); err != nil {
		http.Error(w, "session issue failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.sessions.Logout(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (s *Server) dashboardState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.store.Snapshot()
	var pool any = nil
	var quotaState any = nil
	if s.pool != nil {
		pool = s.pool.Snapshot()
		quotaState = s.pool.QuotaSnapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":      map[string]any{"version": version.Version, "commit": version.Commit, "date": version.Date},
		"config":       config.Redacted(cfg),
		"account_pool": pool,
		"quota_state":  quotaState,
		"metrics":      s.metrics.Snapshot(),
		"tunnel":       s.tunnelStatusSnapshot(),
		"recent_usage": s.recentUsageItems(12),
		"gateway":      map[string]any{"key_configured": s.store.GatewayKey() != "", "keys_count": len(cfg.GatewayKeys)},
	})
}

func (s *Server) debugSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.debugDashboard {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "status": "disabled"})
		return
	}
	cfg := s.store.Snapshot()
	accountStatusCounts := map[string]int{}
	if s.pool != nil {
		for _, account := range s.pool.Snapshot().Accounts {
			accountStatusCounts[account.Status]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":      true,
		"status":       "enabled",
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"version":      map[string]any{"version": version.Version, "commit": version.Commit, "date": version.Date},
		"runtime": map[string]any{
			"debug_dashboard":        true,
			"gateway_key_configured": s.store.GatewayKey() != "",
			"account_pool_ready":     s.pool != nil,
		},
		"config_summary": map[string]any{
			"config_version":        cfg.ConfigVersion,
			"accounts":              len(cfg.Accounts),
			"enabled_accounts":      countEnabledAccounts(cfg.Accounts),
			"oauth_sources":         len(cfg.OAuthSources),
			"subscription_sources":  len(cfg.SubscriptionSources),
			"proxies":               len(cfg.Proxies),
			"quota_policies":        len(cfg.Quota.Policies),
			"routing_rules":         len(cfg.Routing.Rules),
			"account_status_counts": accountStatusCounts,
		},
		"policy_flags": map[string]any{
			"allow_lan":                   cfg.Server.AllowLAN,
			"cors_enabled":                len(cfg.Server.CORSAllowedOrigins) > 0,
			"upstreamcompat_enabled":      cfg.UpstreamCompat.Enabled,
			"probe_save_policy":           cfg.Probe.SavePolicy,
			"metrics_recent_errors_limit": cfg.Metrics.RecentErrorsLimit,
		},
		"debug_events": []any{},
	})
}

func (s *Server) revealGatewayKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.store.Snapshot().GatewayKeys != nil {
		http.Error(w, "gateway key material is hashed and cannot be revealed; use /api/keys rotate to obtain a fresh value", http.StatusGone)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gateway_key": s.store.GatewayKey()})
}

func (s *Server) rotateGatewayKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key, err := s.store.RotateGatewayKey()
	if err != nil {
		http.Error(w, "gateway key rotation failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gateway_key": key})
}

func (s *Server) keys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.keysResponse())
	case http.MethodPost:
		var req keyMutationRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		plaintext := strings.TrimSpace(req.Key)
		if plaintext == "" {
			var err error
			plaintext, err = config.GenerateGatewayKey()
			if err != nil {
				http.Error(w, "gateway key generation failed", http.StatusInternalServerError)
				return
			}
		}
		now := time.Now().UTC().Format(time.RFC3339)
		key := req.GatewayKey
		if strings.TrimSpace(key.ID) == "" {
			key.ID = gatewayKeyIDFromName(key.Name, now)
		}
		if strings.TrimSpace(key.Name) == "" {
			key.Name = "Gateway Key"
		}
		if strings.TrimSpace(key.Status) == "" {
			key.Status = "enabled"
		}
		if strings.TrimSpace(key.RoutingPolicy.Mode) == "" {
			key.RoutingPolicy.Mode = "all_enabled"
		}
		key.KeyHash = config.HashGatewayKey(plaintext)
		key.KeyValue = plaintext
		key.Preview = config.KeyPreview(plaintext)
		key.CreatedAt = now
		key.UpdatedAt = now
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.GatewayKeys = upsertGatewayKey(cfg.GatewayKeys, key)
			return nil
		})
		if err != nil {
			s.writeUpdateResult(w, updated, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"config_version": updated.ConfigVersion, "key": redactedGatewayKey(key), "key_value": plaintext})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) keyByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/keys/"), "/")
	parts := strings.Split(path, "/")
	id := parts[0]
	if id == "" {
		http.Error(w, "key id required", http.StatusBadRequest)
		return
	}
	if len(parts) == 2 && parts[1] == "rotate" {
		s.rotateKeyByID(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "reveal" {
		http.Error(w, "stored key material is hashed and cannot be revealed; rotate to obtain a fresh value", http.StatusGone)
		return
	}
	if len(parts) != 1 {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var req keyMutationRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.GatewayKey.ID == "" {
			req.GatewayKey.ID = id
		}
		if req.GatewayKey.ID != id {
			http.Error(w, "key id mismatch", http.StatusBadRequest)
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			for _, current := range cfg.GatewayKeys {
				if current.ID != id {
					continue
				}
				candidate := req.GatewayKey
				candidate.KeyHash = current.KeyHash
				candidate.KeyValue = current.KeyValue
				candidate.Preview = current.Preview
				candidate.CreatedAt = current.CreatedAt
				candidate.LastUsedAt = current.LastUsedAt
				candidate.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				if strings.TrimSpace(candidate.RoutingPolicy.Mode) == "" {
					candidate.RoutingPolicy.Mode = "all_enabled"
				}
				cfg.GatewayKeys = upsertGatewayKey(cfg.GatewayKeys, candidate)
				return nil
			}
			return errors.New("gateway key not found")
		})
		s.writeUpdateResult(w, updated, err)
	case http.MethodDelete:
		var req versionedRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			if !gatewayKeyExists(cfg.GatewayKeys, id) {
				return errors.New("gateway key not found")
			}
			cfg.GatewayKeys = deleteGatewayKey(cfg.GatewayKeys, id)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.groupsResponse())
	case http.MethodPost:
		var req groupMutationRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		group := req.Group
		if strings.TrimSpace(group.ID) == "" {
			group.ID = groupIDFromName(group.Name, now)
		}
		if strings.TrimSpace(group.Status) == "" {
			group.Status = "active"
		}
		if strings.TrimSpace(group.Platform) == "" {
			group.Platform = "mixed"
		}
		group.Tags = ensureString(group.Tags, group.ID)
		group.CreatedAt = now
		group.UpdatedAt = now
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			groupIDs := groupIDsForAccount(group.AccountIDs, cfg.Groups)
			group.Tags = mergeUnique(group.Tags, groupIDs)
			cfg.Groups = upsertGroup(cfg.Groups, group)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) groupByID(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/groups/"), "/")
	if id == "" {
		http.Error(w, "group id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var req groupMutationRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Group.ID) == "" {
			req.Group.ID = id
		}
		if req.Group.ID != id {
			http.Error(w, "group id mismatch", http.StatusBadRequest)
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			for _, current := range cfg.Groups {
				if current.ID != id {
					continue
				}
				candidate := req.Group
				candidate.CreatedAt = current.CreatedAt
				candidate.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				candidate.Tags = ensureString(candidate.Tags, candidate.ID)
				groupIDs := groupIDsForAccount(candidate.AccountIDs, cfg.Groups)
				candidate.Tags = mergeUnique(candidate.Tags, groupIDs)
				cfg.Groups = upsertGroup(cfg.Groups, candidate)
				return nil
			}
			return errors.New("group not found")
		})
		s.writeUpdateResult(w, updated, err)
	case http.MethodDelete:
		var req versionedRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			if !groupExists(cfg.Groups, id) {
				return errors.New("group not found")
			}
			cfg.Groups = deleteGroup(cfg.Groups, id)
			removeGroupFromGatewayPolicies(cfg.GatewayKeys, id)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) rotateKeyByID(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req versionedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	plaintext, err := config.GenerateGatewayKey()
	if err != nil {
		http.Error(w, "gateway key generation failed", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var rotated config.GatewayKey
	updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
		for _, current := range cfg.GatewayKeys {
			if current.ID != id {
				continue
			}
			rotated = current
			rotated.KeyHash = config.HashGatewayKey(plaintext)
			rotated.KeyValue = plaintext
			rotated.Preview = config.KeyPreview(plaintext)
			rotated.UpdatedAt = now
			rotated.LastUsedAt = ""
			cfg.GatewayKeys = upsertGatewayKey(cfg.GatewayKeys, rotated)
			return nil
		}
		return errors.New("gateway key not found")
	})
	if err != nil {
		s.writeUpdateResult(w, updated, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config_version": updated.ConfigVersion, "key": redactedGatewayKey(rotated), "key_value": plaintext})
}

func (s *Server) configState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, config.Redacted(s.store.Snapshot()))
}

func (s *Server) configSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ConfigVersion int           `json:"config_version"`
		Config        config.Config `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
		candidate := req.Config
		preserveRedactedSecrets(&candidate, *cfg)
		*cfg = candidate
		return nil
	})
	s.writeUpdateResult(w, updated, err)
}

func (s *Server) tunnelStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.tunnelStatusSnapshot())
}

func (s *Server) tunnelConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req tunnelConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
		candidate := req.Tunnel
		if isMasked(candidate.Token) {
			candidate.Token = cfg.Tunnel.Token
		}
		cfg.Tunnel = candidate
		return nil
	})
	if err != nil {
		s.writeUpdateResult(w, updated, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config_version": updated.ConfigVersion, "tunnel": config.Redacted(updated).Tunnel, "status": s.tunnelStatusSnapshot()})
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.accountsResponse())
	case http.MethodPost:
		var req accountMutationRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		account := req.Account
		if account.ID == "" && req.isUpstreamCreate() {
			account = req.toJSONAccount(time.Now().UTC().Format(time.RFC3339))
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.Accounts = upsertAccount(cfg.Accounts, account)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) accountByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/accounts/")
	if strings.TrimSpace(path) == "" {
		http.Error(w, "account id required", http.StatusBadRequest)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id := parts[0]
	if len(parts) == 2 && (parts[1] == "refresh" || parts[1] == "test") {
		s.accountRefresh(w, r, id)
		return
	}
	if len(parts) != 1 {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.Method {
	case http.MethodGet:
		for _, account := range config.Redacted(s.store.Snapshot()).Accounts {
			if account.ID == id {
				writeJSON(w, http.StatusOK, account)
				return
			}
		}
		http.Error(w, "account not found", http.StatusNotFound)
	case http.MethodPut:
		var req accountMutationRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Account.ID) == "" {
			req.Account.ID = id
		}
		if req.Account.ID != id {
			http.Error(w, "account id mismatch", http.StatusBadRequest)
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			preserveRedactedAccountSecret(&req.Account, cfg.Accounts)
			if !accountExists(cfg.Accounts, id) {
				return errors.New("account not found")
			}
			cfg.Accounts = upsertAccount(cfg.Accounts, req.Account)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	case http.MethodPatch:
		var req accountPatchRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			for i := range cfg.Accounts {
				if cfg.Accounts[i].ID != id {
					continue
				}
				if req.Enabled != nil {
					cfg.Accounts[i].Enabled = *req.Enabled
				}
				return nil
			}
			return errors.New("account not found")
		})
		s.writeUpdateResult(w, updated, err)
	case http.MethodDelete:
		var req versionedRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			if !accountExists(cfg.Accounts, id) {
				return errors.New("account not found")
			}
			cfg.Accounts = deleteAccount(cfg.Accounts, id)
			removeAccountFromGroups(cfg.Groups, id)
			removeAccountFromGatewayPolicies(cfg.GatewayKeys, id)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) accountRefresh(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.logger != nil {
		s.logger.Info("account_refresh_start", slog.String("account_id", id))
	}
	cfg := s.store.Snapshot()
	for _, account := range cfg.Accounts {
		if account.ID == id {
			if s.logger != nil {
				s.logger.Info(
					"account_refresh_account_found",
					slog.String("account_id", id),
					slog.String("type", account.Type),
					slog.String("platform", config.AccountPlatform(account)),
					slog.Bool("enabled", account.Enabled),
				)
			}
			result := accountcheck.New(cfg.Probe).Check(r.Context(), cfg, account)
			if s.logger != nil {
				s.logger.Info(
					"account_refresh_health_checked",
					slog.String("account_id", id),
					slog.String("status", result.Status),
					slog.String("message", result.Message),
					slog.String("proxy_id", result.ProxyID),
				)
			}
			displaySync := s.syncAccountDisplayState(r.Context(), cfg, &account)
			if s.logger != nil {
				s.logger.Info(
					"account_refresh_display_sync_result",
					slog.String("account_id", id),
					slog.Bool("synced", displaySync.Synced),
					slog.String("message", displaySync.Message),
					slog.String("credential_source", displaySync.CredentialSource),
					slog.String("subscription_tier", displaySync.Tier),
					slog.Any("usage_info_keys", displaySync.UsageInfoKeys),
					slog.String("subscription_error", displaySync.SubscriptionErr),
					slog.String("usage_error", displaySync.UsageErr),
				)
			}
			if displaySync.Synced {
				cfg = replaceAccountInConfig(cfg, account)
			}
			if s.pool != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Probe.TimeoutSeconds)*time.Second)
				defer cancel()
				_ = s.pool.ApplyConfig(ctx, cfg)
			}
			accounts := s.accountsResponseFromConfig(cfg)
			response := accountRefreshResponse{
				AccountID: result.AccountID,
				Status:    result.Status,
				Message:   joinRefreshMessages(result.Message, displaySync.Message),
				CheckedAt: result.CheckedAt,
				ProxyID:   result.ProxyID,
				Health:    result,
				Accounts:  &accounts,
			}
			for i := range accounts.Accounts {
				if accounts.Accounts[i].Config.ID == id {
					response.Account = &accounts.Accounts[i]
					break
				}
			}
			writeJSON(w, http.StatusOK, response)
			if s.logger != nil {
				s.logger.Info(
					"account_refresh_response_written",
					slog.String("account_id", id),
					slog.String("status", response.Status),
					slog.Bool("has_account", response.Account != nil),
					slog.Bool("has_accounts_snapshot", response.Accounts != nil),
				)
			}
			return
		}
	}
	if s.logger != nil {
		s.logger.Warn("account_refresh_not_found", slog.String("account_id", id))
	}
	http.Error(w, "account not found", http.StatusNotFound)
}

func (s *Server) openAIOAuthExchangeCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req openAIOAuthExchangeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		http.Error(w, "authorization code is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.CodeVerifier) == "" {
		http.Error(w, "code verifier is required; regenerate the authorization URL and retry", http.StatusBadRequest)
		return
	}
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		clientID = openAIOAuthClientID
	}
	redirectURI := strings.TrimSpace(req.RedirectURI)
	if redirectURI == "" {
		redirectURI = openAIOAuthRedirectURI
	}
	tokenURL := strings.TrimSpace(req.TokenURL)
	if tokenURL == "" {
		tokenURL = openAIOAuthTokenURL
	}
	client, err := s.openAIOAuthHTTPClient(r.Context(), req.ProxyRef)
	if err != nil {
		http.Error(w, sanitizeOpenAIOAuthExchangeError(err), http.StatusBadGateway)
		return
	}
	token, err := exchangeOpenAIOAuthCode(r.Context(), client, openAIOAuthExchangeInput{
		Code:         strings.TrimSpace(req.Code),
		CodeVerifier: strings.TrimSpace(req.CodeVerifier),
		RedirectURI:  redirectURI,
		ClientID:     clientID,
		TokenURL:     tokenURL,
	})
	if err != nil {
		http.Error(w, sanitizeOpenAIOAuthExchangeError(err), http.StatusBadGateway)
		return
	}
	credentials := map[string]string{
		"access_token":  token.AccessToken,
		"refresh_token": token.RefreshToken,
		"expires_at":    time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339),
		"client_id":     clientID,
	}
	if token.ExpiresIn <= 0 {
		credentials["expires_at"] = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	}
	if strings.TrimSpace(token.IDToken) != "" {
		credentials["id_token"] = strings.TrimSpace(token.IDToken)
	}
	writeJSON(w, http.StatusOK, openAIOAuthExchangeResponse{Credentials: credentials, ExpiresAt: credentials["expires_at"]})
}

func (s *Server) openAIOAuthHTTPClient(ctx context.Context, proxyRef string) (*http.Client, error) {
	cfg := s.store.Snapshot()
	timeout := time.Duration(cfg.Probe.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	account := config.Account{ID: "openai_oauth_exchange", ProxyRef: strings.TrimSpace(proxyRef)}
	spec, _, err := proxyclient.Resolve(account, proxyclient.SpecsFromConfig(cfg))
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return proxyclient.HTTPClient(spec, timeout)
	}
}

func (s *Server) oauthSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, config.Redacted(s.store.Snapshot()).OAuthSources)
	case http.MethodPost:
		var req sourceMutationRequest[config.OAuthSource]
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.OAuthSources = upsertOAuthSource(cfg.OAuthSources, req.Source)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) oauthSourceByID(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/oauth-sources/"), "/")
	parts := strings.Split(path, "/")
	id := parts[0]
	if id == "" {
		http.Error(w, "source id required", http.StatusBadRequest)
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "refresh":
			s.oauthSourceRefresh(w, r, id)
			return
		case "reauth":
			s.oauthSourceReauth(w, r, id)
			return
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	}
	if len(parts) != 1 {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.Method {
	case http.MethodGet:
		impact := subscriptionimport.ImpactForSource(id, s.store.Snapshot())
		writeJSON(w, http.StatusOK, impact)
	case http.MethodDelete:
		var req versionedRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.OAuthSources = deleteOAuthSource(cfg.OAuthSources, id)
			for i := range cfg.Accounts {
				if cfg.Accounts[i].SourceID == id {
					cfg.Accounts[i].Enabled = false
				}
			}
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) oauthSourceRefresh(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req versionedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
		for i := range cfg.OAuthSources {
			if cfg.OAuthSources[i].ID != id {
				continue
			}
			if !cfg.OAuthSources[i].Enabled {
				cfg.OAuthSources[i].RefreshState.Status = "disabled"
				cfg.OAuthSources[i].RefreshState.LastError = "source disabled"
				return nil
			}
			if len(cfg.OAuthSources[i].Credentials) == 0 {
				cfg.OAuthSources[i].RefreshState.Status = "needs_auth"
				cfg.OAuthSources[i].RefreshState.LastError = "credentials required for provider refresh"
				return nil
			}
			cfg.OAuthSources[i].RefreshState.Status = "ok"
			cfg.OAuthSources[i].RefreshState.LastRefreshAt = now
			cfg.OAuthSources[i].RefreshState.LastError = ""
			return nil
		}
		return errors.New("oauth source not found")
	})
	s.writeUpdateResult(w, updated, err)
}

func (s *Server) oauthSourceReauth(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req versionedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
		for i := range cfg.OAuthSources {
			if cfg.OAuthSources[i].ID != id {
				continue
			}
			cfg.OAuthSources[i].RefreshState.Status = "needs_auth"
			cfg.OAuthSources[i].RefreshState.LastError = "reauth requested; complete provider authorization outside this local dashboard"
			return nil
		}
		return errors.New("oauth source not found")
	})
	s.writeUpdateResult(w, updated, err)
}

func (s *Server) subscriptionSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, config.Redacted(s.store.Snapshot()).SubscriptionSources)
	case http.MethodPost:
		var req sourceMutationRequest[config.SubscriptionSource]
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.SubscriptionSources = upsertSubscriptionSource(cfg.SubscriptionSources, req.Source)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) subscriptionSourceByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/subscription-sources/")
	if id == "" {
		http.Error(w, "source id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		impact := subscriptionimport.ImpactForSource(id, s.store.Snapshot())
		writeJSON(w, http.StatusOK, impact)
	case http.MethodDelete:
		var req versionedRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.SubscriptionSources = deleteSubscriptionSource(cfg.SubscriptionSources, id)
			for i := range cfg.Accounts {
				if cfg.Accounts[i].SourceID == id {
					cfg.Accounts[i].Enabled = false
				}
			}
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) importPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req subscriptionimport.Request
	if !decodeJSON(w, r, &req) {
		return
	}
	preview, err := subscriptionimport.PreviewImport(r.Context(), req, s.store.Snapshot())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) importApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ConfigVersion int                        `json:"config_version"`
		Import        subscriptionimport.Request `json:"import"`
		Preview       subscriptionimport.Preview `json:"preview"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	preview := req.Preview
	if req.Import.Kind != "" {
		prepared, err := subscriptionimport.PrepareImport(r.Context(), req.Import, s.store.Snapshot(), false)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		preview = prepared
	}
	updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
		subscriptionimport.ApplyPreview(cfg, preview)
		return nil
	})
	s.writeUpdateResult(w, updated, err)
}

func (s *Server) accountsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, config.Redacted(s.store.Snapshot()))
}

func (s *Server) proxies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.Snapshot().Proxies)
	case http.MethodPost:
		var req struct {
			ConfigVersion int                `json:"config_version"`
			Proxy         config.ProxyConfig `json:"proxy"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.Proxies = upsertProxy(cfg.Proxies, req.Proxy)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) proxyByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/proxies/")
	if id == "" {
		http.Error(w, "proxy id required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPut {
		var req struct {
			ConfigVersion int                `json:"config_version"`
			Proxy         config.ProxyConfig `json:"proxy"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Proxy.ID == "" {
			req.Proxy.ID = id
		}
		if req.Proxy.ID != id {
			http.Error(w, "proxy id mismatch", http.StatusBadRequest)
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.Proxies = upsertProxy(cfg.Proxies, req.Proxy)
			return nil
		})
		s.writeUpdateResult(w, updated, err)
		return
	}
	var req versionedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
		cfg.Proxies = deleteProxy(cfg.Proxies, id)
		for i := range cfg.Accounts {
			if cfg.Accounts[i].ProxyRef == id {
				cfg.Accounts[i].Enabled = false
				cfg.Accounts[i].ProxyRef = ""
			}
		}
		return nil
	})
	s.writeUpdateResult(w, updated, err)
}

func (s *Server) quotaConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.Snapshot().Quota)
	case http.MethodPost:
		var req struct {
			ConfigVersion int                `json:"config_version"`
			Quota         config.QuotaConfig `json:"quota"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.Quota = req.Quota
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) quotaState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		http.Error(w, "account pool unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, s.pool.QuotaSnapshot())
}

func (s *Server) routingConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.Snapshot().Routing)
	case http.MethodPost:
		var req struct {
			ConfigVersion int                  `json:"config_version"`
			Routing       config.RoutingConfig `json:"routing"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
			cfg.Routing = req.Routing
			return nil
		})
		s.writeUpdateResult(w, updated, err)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) routingDecide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req routing.Request
	if !decodeJSON(w, r, &req) {
		return
	}
	decision := routing.Decide(s.store.Snapshot().Routing, req)
	s.metrics.RecordRoutingDecision(metrics.RoutingDecision{TaskType: decision.TaskType, MatchedRule: decision.MatchedRule, PreferTiers: decision.PreferTiers, FallbackTiers: decision.FallbackTiers, Reason: "admin routing test"})
	writeJSON(w, http.StatusOK, decision)
}

func (s *Server) accountCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.store.Snapshot()
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, accountcheck.CheckAll(r.Context(), cfg))
		return
	}
	var req struct {
		AccountID string `json:"account_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	for _, account := range cfg.Accounts {
		if account.ID == req.AccountID {
			writeJSON(w, http.StatusOK, accountcheck.New(cfg.Probe).Check(r.Context(), cfg, account))
			return
		}
	}
	http.Error(w, "account not found", http.StatusNotFound)
}

func (s *Server) accountPool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		http.Error(w, "account pool unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, s.pool.Snapshot())
}

func (s *Server) metricsSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.metrics.Snapshot())
}

func (s *Server) recentUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"limit":        12,
		"top_usage":    s.recentUsageItems(12),
	})
}

func (s *Server) recentUsageItems(limit int) []recentUsageItem {
	if limit <= 0 {
		limit = 12
	}
	cfg := s.store.Snapshot()
	accountLabels := make(map[string]string, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		accountLabels[account.ID] = account.Label
	}
	keyLabels := make(map[string]string, len(cfg.GatewayKeys))
	keyPreviews := make(map[string]string, len(cfg.GatewayKeys))
	for _, key := range cfg.GatewayKeys {
		keyLabels[key.ID] = key.Name
		keyPreviews[key.ID] = key.Preview
	}
	aggregates := s.metrics.TopUsage(limit)
	items := make([]recentUsageItem, 0, len(aggregates))
	for index, aggregate := range aggregates {
		preview := aggregate.GatewayKeyRef
		if preview == "" {
			preview = keyPreviews[aggregate.GatewayKeyID]
		}
		successRate := float64(0)
		if aggregate.Requests > 0 {
			successRate = float64(aggregate.Successes) / float64(aggregate.Requests)
		}
		items = append(items, recentUsageItem{
			Rank:              index + 1,
			AccountID:         aggregate.AccountID,
			AccountLabel:      accountLabels[aggregate.AccountID],
			GatewayKeyID:      aggregate.GatewayKeyID,
			GatewayKeyPreview: preview,
			GatewayKeyLabel:   keyLabels[aggregate.GatewayKeyID],
			Requests:          aggregate.Requests,
			Successes:         aggregate.Successes,
			Errors:            aggregate.Errors,
			SuccessRate:       successRate,
			LastUsedAt:        aggregate.LastUsedAt,
		})
	}
	return items
}

func (s *Server) adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.sessions.Authenticate(r) {
			http.Error(w, "admin session required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) updateConfig(expectedVersion int, mutate func(*config.Config) error) (config.Config, error) {
	updated, err := s.store.UpdateValidated(expectedVersion, mutate, func(candidate config.Config) error {
		if s.pool == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(candidate.Probe.TimeoutSeconds)*time.Second)
		defer cancel()
		return s.pool.ValidateConfig(ctx, candidate)
	})
	if err != nil {
		return config.Config{}, err
	}
	if s.pool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(updated.Probe.TimeoutSeconds)*time.Second)
		defer cancel()
		if err := s.pool.ApplyConfig(ctx, updated); err != nil {
			return config.Config{}, err
		}
	}
	s.metrics.ApplyRecentErrorsLimit(updated.Metrics.RecentErrorsLimit)
	if err := s.applyTunnelConfig(updated); err != nil {
		if isMissingTunnelBinaryError(err) {
			if s.logger != nil {
				s.logger.Warn("tunnel_apply_skipped_missing_binary", slog.String("error", err.Error()))
			}
			return updated, nil
		}
		return config.Config{}, err
	}
	return updated, nil
}

func isMissingTunnelBinaryError(err error) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	return errors.Is(err, exec.ErrNotFound) || (errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound))
}

func (s *Server) applyTunnelConfig(cfg config.Config) error {
	if s.tunnel == nil {
		return nil
	}
	return s.tunnel.Apply(cfg.Tunnel, cfg.Server.Bind)
}

func (s *Server) tunnelStatusSnapshot() tunnel.RuntimeStatus {
	if s.tunnel == nil {
		return tunnel.RuntimeStatus{Status: tunnel.StatusStopped}
	}
	return s.tunnel.Status()
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			cfg := s.store.Snapshot()
			if !originAllowed(origin, cfg.Server.CORSAllowedOrigins) {
				http.Error(w, "CORS origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) matchesAdminPassword(password string) bool {
	cfg := s.store.Snapshot()
	configured := cfg.Dashboard.AdminPassword
	if configured == "" && config.IsLoopbackBind(cfg.Server.Bind) {
		return password == ""
	}
	if len(password) != len(configured) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(configured)) == 1
}

func originAllowed(origin string, allowed []string) bool {
	for _, candidate := range allowed {
		if origin == candidate {
			return true
		}
	}
	return false
}

type versionedRequest struct {
	ConfigVersion int `json:"config_version"`
}

type accountMutationRequest struct {
	ConfigVersion           int            `json:"config_version"`
	Account                 config.Account `json:"account"`
	ID                      string         `json:"id"`
	Name                    string         `json:"name"`
	Notes                   *string        `json:"notes"`
	Platform                string         `json:"platform"`
	Type                    string         `json:"type"`
	Credentials             map[string]any `json:"credentials"`
	Extra                   map[string]any `json:"extra"`
	ProxyID                 any            `json:"proxy_id"`
	Concurrency             int            `json:"concurrency"`
	LoadFactor              any            `json:"load_factor"`
	Priority                int            `json:"priority"`
	RateMultiplier          any            `json:"rate_multiplier"`
	GroupIDs                []any          `json:"group_ids"`
	ExpiresAt               any            `json:"expires_at"`
	AutoPauseOnExpired      *bool          `json:"auto_pause_on_expired"`
	ConfirmMixedChannelRisk *bool          `json:"confirm_mixed_channel_risk"`
}

func (r accountMutationRequest) isUpstreamCreate() bool {
	return strings.TrimSpace(r.Name) != "" && strings.TrimSpace(r.Platform) != "" && strings.TrimSpace(r.Type) != "" && len(r.Credentials) > 0
}

func (r accountMutationRequest) toJSONAccount(now string) config.Account {
	platform := strings.TrimSpace(r.Platform)
	accountType := simpleAccountTypeFromUpstream(platform, strings.TrimSpace(r.Type))
	metadata := map[string]any{}
	for key, value := range r.Extra {
		metadata[key] = value
	}
	metadata["upstream_create_account_compat"] = true
	metadata["platform"] = platform
	metadata["account_category"] = accountCategoryFromUpstreamType(strings.TrimSpace(r.Type))
	metadata["upstream_type"] = strings.TrimSpace(r.Type)
	if r.Notes != nil && strings.TrimSpace(*r.Notes) != "" {
		metadata["notes"] = strings.TrimSpace(*r.Notes)
	}
	if r.Concurrency > 0 {
		metadata["concurrency"] = r.Concurrency
	}
	if loadFactor := numberFromAny(r.LoadFactor); loadFactor > 0 {
		metadata["load_factor"] = loadFactor
	}
	if r.Priority > 0 {
		metadata["priority"] = r.Priority
	}
	if rateMultiplier := numberFromAny(r.RateMultiplier); rateMultiplier >= 0 {
		metadata["rate_multiplier"] = rateMultiplier
	}
	if r.AutoPauseOnExpired != nil {
		metadata["auto_pause_on_expired"] = *r.AutoPauseOnExpired
	}
	if expiresAt := expiresAtString(r.ExpiresAt); expiresAt != "" {
		metadata["expires_at"] = expiresAt
	}
	if modelMapping, ok := r.Credentials["model_mapping"]; ok && modelMapping != nil {
		metadata["model_mapping"] = modelMapping
		metadata["model_mappings"] = modelMappingRowsFromAny(modelMapping)
	}
	if compactMapping, ok := r.Credentials["compact_model_mapping"]; ok && compactMapping != nil {
		metadata["openai_compact_mappings"] = compactMapping
	}

	credential := credentialEnvelopeFromMap(r.Credentials)
	baseURL := credentialString(r.Credentials, "base_url")
	proxyRef := stringFromAny(r.ProxyID)
	groupIDs := stringSliceFromAny(r.GroupIDs)
	if len(groupIDs) > 0 {
		metadata["group_ids"] = groupIDs
		metadata["groups"] = groupIDs
	}

	accountID := strings.TrimSpace(r.ID)
	if accountID == "" {
		accountID = accountIDFromName(r.Name, now)
	}

	return config.Account{
		ID:         accountID,
		Type:       accountType,
		Label:      strings.TrimSpace(r.Name),
		BaseURL:    baseURL,
		Tier:       tierFromCreateRequest(platform, r.Credentials, r.Extra),
		Tags:       groupIDs,
		Credential: credential,
		Metadata:   metadata,
		ProxyRef:   proxyRef,
		Enabled:    true,
	}
}

func simpleAccountTypeFromUpstream(platform, upstreamType string) string {
	switch strings.TrimSpace(upstreamType) {
	case "oauth", "setup-token":
		return "oauth"
	case "upstream":
		return "openai_compatible"
	}
	if strings.TrimSpace(platform) == "openai" {
		return "openai_api_key"
	}
	return "openai_compatible"
}

func accountCategoryFromUpstreamType(upstreamType string) string {
	switch strings.TrimSpace(upstreamType) {
	case "oauth", "setup-token":
		return "oauth-based"
	case "bedrock":
		return "bedrock"
	case "service_account":
		return "service_account"
	case "upstream":
		return "upstream"
	default:
		return "apikey"
	}
}

func modelMappingRowsFromAny(value any) []map[string]string {
	rows := []map[string]string{}
	if mapping, ok := value.(map[string]any); ok {
		for from, rawTo := range mapping {
			to := strings.TrimSpace(stringFromAny(rawTo))
			from = strings.TrimSpace(from)
			if from == "" || to == "" {
				continue
			}
			rows = append(rows, map[string]string{"from": from, "to": to})
		}
	}
	return rows
}

func credentialEnvelopeFromMap(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	preferred := []string{
		"api_key", "refresh_token", "setup_token", "access_token", "session_token", "codex_session",
		"service_account_json", "auth_mode", "aws_access_key_id", "aws_secret_access_key", "aws_session_token", "aws_region",
		"base_url", "project_id", "location", "client_email", "tier_id", "token_type", "expires_at", "client_id",
	}
	seen := map[string]bool{}
	parts := make([]string, 0, len(values))
	add := func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		value := stringFromAny(values[key])
		if strings.TrimSpace(value) == "" {
			return
		}
		parts = append(parts, key+"="+value)
	}
	for _, key := range preferred {
		add(key)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		add(key)
	}
	return strings.Join(parts, ";")
}

func credentialString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	return stringFromAny(values[key])
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		return strconv.FormatBool(v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func numberFromAny(value any) float64 {
	switch v := value.(type) {
	case nil:
		return -1
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		parsed, err := v.Float64()
		if err != nil {
			return -1
		}
		return parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return -1
		}
		return parsed
	default:
		return -1
	}
}

func stringSliceFromAny(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text := stringFromAny(value)
		if text != "" {
			out = ensureString(out, text)
		}
	}
	return out
}

func expiresAtString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v <= 0 {
			return ""
		}
		return time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
	case int64:
		if v <= 0 {
			return ""
		}
		return time.Unix(v, 0).UTC().Format(time.RFC3339)
	case int:
		if v <= 0 {
			return ""
		}
		return time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
	default:
		return ""
	}
}

func tierFromCreateRequest(platform string, credentials map[string]any, extra map[string]any) string {
	for _, key := range []string{"tier", "tier_id", "gemini_tier"} {
		if value := credentialString(credentials, key); value != "" {
			return value
		}
		if value := stringFromAny(extra[key]); value != "" {
			return value
		}
	}
	if strings.TrimSpace(platform) == "gemini" {
		return "aistudio_free"
	}
	return "simple"
}

type accountPatchRequest struct {
	ConfigVersion int   `json:"config_version"`
	Enabled       *bool `json:"enabled,omitempty"`
}

type groupMutationRequest struct {
	ConfigVersion int          `json:"config_version"`
	Group         config.Group `json:"group"`
}

type sourceMutationRequest[T any] struct {
	ConfigVersion int `json:"config_version"`
	Source        T   `json:"source"`
}

type keyMutationRequest struct {
	ConfigVersion int               `json:"config_version"`
	GatewayKey    config.GatewayKey `json:"key"`
	Key           string            `json:"key_value,omitempty"`
}

type tunnelConfigRequest struct {
	ConfigVersion int                 `json:"config_version"`
	Tunnel        config.TunnelConfig `json:"tunnel"`
}

type recentUsageItem struct {
	Rank              int     `json:"rank"`
	AccountID         string  `json:"account_id"`
	AccountLabel      string  `json:"account_label,omitempty"`
	GatewayKeyID      string  `json:"gateway_key_id,omitempty"`
	GatewayKeyPreview string  `json:"gateway_key_preview,omitempty"`
	GatewayKeyLabel   string  `json:"gateway_key_label,omitempty"`
	Requests          uint64  `json:"requests"`
	Successes         uint64  `json:"successes"`
	Errors            uint64  `json:"errors"`
	SuccessRate       float64 `json:"success_rate"`
	LastUsedAt        string  `json:"last_used_at,omitempty"`
}

type keysResponse struct {
	ConfigVersion int                 `json:"config_version"`
	Keys          []config.GatewayKey `json:"keys"`
	Accounts      []config.Account    `json:"accounts"`
	Groups        []config.Group      `json:"groups"`
}

type groupsResponse struct {
	ConfigVersion int              `json:"config_version"`
	Groups        []groupSummary   `json:"groups"`
	Accounts      []config.Account `json:"accounts"`
}

type groupSummary struct {
	Config       config.Group `json:"config"`
	AccountCount int          `json:"account_count"`
}

type accountSummary struct {
	Config            config.Account       `json:"config"`
	RuntimeStatus     string               `json:"runtime_status"`
	Health            *accountcheck.Result `json:"health,omitempty"`
	Quota             map[string]any       `json:"quota,omitempty"`
	Metrics           map[string]uint64    `json:"metrics,omitempty"`
	Runtime           map[string]any       `json:"runtime,omitempty"`
	Source            string               `json:"source,omitempty"`
	References        map[string]string    `json:"references,omitempty"`
	SubscriptionTier  string               `json:"subscription_tier,omitempty"`
	PrivacyMode       string               `json:"privacy_mode,omitempty"`
	OpenAICompactMode string               `json:"openai_compact_mode,omitempty"`
	UsageInfo         map[string]any       `json:"usage_info,omitempty"`
}

type accountsResponse struct {
	ConfigVersion int                  `json:"config_version"`
	Accounts      []accountSummary     `json:"accounts"`
	Proxies       []config.ProxyConfig `json:"proxies"`
	Groups        []config.Group       `json:"groups"`
	QuotaPolicies []config.QuotaPolicy `json:"quota_policies"`
	Sources       []string             `json:"sources"`
}

type accountRefreshResponse struct {
	AccountID string              `json:"account_id"`
	Status    string              `json:"status"`
	Message   string              `json:"message,omitempty"`
	CheckedAt string              `json:"checked_at"`
	ProxyID   string              `json:"proxy_id,omitempty"`
	Health    accountcheck.Result `json:"health"`
	Account   *accountSummary     `json:"account,omitempty"`
	Accounts  *accountsResponse   `json:"accounts,omitempty"`
}

type openAIOAuthExchangeRequest struct {
	Code         string `json:"code"`
	State        string `json:"state,omitempty"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	TokenURL     string `json:"token_url,omitempty"`
	ProxyRef     string `json:"proxy_ref,omitempty"`
}

type openAIOAuthExchangeResponse struct {
	Credentials map[string]string `json:"credentials"`
	ExpiresAt   string            `json:"expires_at"`
}

type openAIOAuthExchangeInput struct {
	Code         string
	CodeVerifier string
	RedirectURI  string
	ClientID     string
	TokenURL     string
}

type openAIOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
}

type accountDisplaySyncResult struct {
	Message          string
	Synced           bool
	Tier             string
	UsageInfoKeys    []string
	SubscriptionErr  string
	UsageErr         string
	CredentialSource string
}

func (s *Server) accountsResponse() accountsResponse {
	return s.accountsResponseFromConfig(s.store.Snapshot())
}

func (s *Server) accountsResponseFromConfig(cfg config.Config) accountsResponse {
	redacted := config.Redacted(cfg)
	poolByID := map[string]accountpool.AccountState{}
	if s.pool != nil {
		for _, state := range s.pool.Snapshot().Accounts {
			poolByID[state.AccountID] = state
		}
	}
	quotaByID := map[string]map[string]any{}
	if s.pool != nil {
		for _, quotaState := range s.pool.QuotaSnapshot() {
			quotaByID[quotaState.AccountID] = map[string]any{
				"account_id":          quotaState.AccountID,
				"status":              quotaState.Status,
				"policy_id":           quotaState.PolicyID,
				"daily_used_tokens":   quotaState.DailyUsed,
				"weekly_used_tokens":  quotaState.WeeklyUsed,
				"daily_limit_tokens":  quotaState.DailyLimit,
				"weekly_limit_tokens": quotaState.WeeklyLimit,
				"usage_ratio":         quotaState.UsageRatio,
				"switch_blocked":      quotaState.SwitchBlocked,
				"error":               quotaState.Error,
			}
		}
	}
	metricSnapshot := s.metrics.Snapshot()
	summaries := make([]accountSummary, 0, len(redacted.Accounts))
	for _, account := range redacted.Accounts {
		summary := accountSummary{Config: account, RuntimeStatus: "unknown"}
		if state, ok := poolByID[account.ID]; ok {
			health := state.Check
			summary.RuntimeStatus = state.Status
			summary.Health = &health
			summary.Runtime = map[string]any{
				"active_conns":      state.ActiveConns,
				"cooldown_until":    state.CooldownUntil,
				"last_selected_at":  state.LastSelectedAt,
				"last_selected_seq": state.LastSelectedSeq,
				"tier":              state.Tier,
				"tags":              state.Tags,
			}
		}
		if quotaState, ok := quotaByID[account.ID]; ok {
			summary.Quota = quotaState
		}
		applyDisplayState(&summary, account)
		summary.Metrics = map[string]uint64{
			"hits":   metricSnapshot.PerAccountHits[account.ID],
			"errors": metricSnapshot.PerAccountErrors[account.ID],
		}
		if account.SourceID != "" {
			summary.Source = account.SourceID
		}
		summary.References = map[string]string{"proxy_ref": account.ProxyRef, "quota_policy": account.QuotaPolicy}
		summaries = append(summaries, summary)
	}
	sources := make([]string, 0, len(cfg.OAuthSources)+len(cfg.SubscriptionSources))
	for _, source := range cfg.OAuthSources {
		sources = append(sources, source.ID)
	}
	for _, source := range cfg.SubscriptionSources {
		sources = append(sources, source.ID)
	}
	return accountsResponse{ConfigVersion: redacted.ConfigVersion, Accounts: summaries, Proxies: redacted.Proxies, Groups: redacted.Groups, QuotaPolicies: redacted.Quota.Policies, Sources: sources}
}

func applyDisplayState(summary *accountSummary, account config.Account) {
	metadata := account.Metadata
	usageInfo := mapFromMetadata(metadata, "usage_info", "usage")
	if len(usageInfo) > 0 {
		summary.UsageInfo = cloneDisplayMap(usageInfo)
	}
	summary.SubscriptionTier = normalizeSubscriptionTier(firstMetadataString(metadata, []string{"subscription_tier", "subscriptionTier", "paid_tier", "current_tier", "plan_type", "tier"}, account.Tier))
	summary.PrivacyMode = firstMetadataString(metadata, []string{"privacy_mode", "privacyMode", "openai_privacy_mode", "training_mode"}, "")
	if summary.PrivacyMode == "" && metadataBool(metadata, "openai_passthrough") {
		summary.PrivacyMode = "private"
	}
	summary.OpenAICompactMode = firstMetadataString(metadata, []string{"openai_compact_mode", "compact_mode", "compactMode"}, "")
	if summary.OpenAICompactMode == "" && config.AccountPlatform(account) == "openai" {
		summary.OpenAICompactMode = "auto"
	}
	if summary.Quota == nil {
		summary.Quota = map[string]any{}
	}
	if summary.SubscriptionTier != "" {
		summary.Quota["subscription_tier"] = summary.SubscriptionTier
	}
	if summary.PrivacyMode != "" {
		summary.Quota["privacy_mode"] = summary.PrivacyMode
	}
	if summary.OpenAICompactMode != "" {
		summary.Quota["openai_compact_mode"] = summary.OpenAICompactMode
	}
	if summary.UsageInfo == nil {
		summary.UsageInfo = usageInfoFromQuotaAndMetadata(summary.Quota, metadata)
	}
	if len(summary.UsageInfo) > 0 {
		summary.Quota["usage_info"] = summary.UsageInfo
	}
}

func replaceAccountInConfig(cfg config.Config, account config.Account) config.Config {
	for i := range cfg.Accounts {
		if cfg.Accounts[i].ID == account.ID {
			cfg.Accounts[i] = account
			return cfg
		}
	}
	return cfg
}

func (s *Server) syncAccountDisplayState(ctx context.Context, cfg config.Config, account *config.Account) accountDisplaySyncResult {
	if account == nil || config.AccountPlatform(*account) != "openai" {
		return accountDisplaySyncResult{}
	}
	accessToken := credentialValue(account.Credential, "access_token")
	credentialSource := "access_token"
	if accessToken == "" {
		accessToken = credentialValue(account.Credential, "api_key")
		credentialSource = "api_key"
	}
	if accessToken == "" && strings.HasPrefix(strings.TrimSpace(account.Credential), "sk-") {
		accessToken = strings.TrimSpace(account.Credential)
		credentialSource = "raw_api_key"
	}
	if accessToken == "" {
		return accountDisplaySyncResult{Message: "display sync skipped: access token or API key is required"}
	}
	client, err := displaySyncHTTPClient(cfg, *account)
	if err != nil {
		return accountDisplaySyncResult{Message: "display sync skipped: " + err.Error(), CredentialSource: credentialSource}
	}
	metadata := cloneDisplayMap(account.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	result := accountDisplaySyncResult{Message: "display sync completed", Synced: true, CredentialSource: credentialSource}
	if tier, err := fetchOpenAISubscriptionTier(ctx, client, accessToken); err == nil && tier != "" {
		metadata["subscription_tier"] = tier
		result.Tier = tier
	} else if err != nil {
		result.SubscriptionErr = sanitizeDisplaySyncError(err)
		metadata["display_sync_subscription_error"] = result.SubscriptionErr
	}
	if usageInfo, err := fetchOpenAIUsageInfo(ctx, client, accessToken); err == nil && len(usageInfo) > 0 {
		metadata["usage_info"] = usageInfo
		result.UsageInfoKeys = sortedMapKeys(usageInfo)
	} else if err != nil {
		result.UsageErr = sanitizeDisplaySyncError(err)
		metadata["display_sync_usage_error"] = result.UsageErr
	}
	account.Metadata = metadata
	return result
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func displaySyncHTTPClient(cfg config.Config, account config.Account) (*http.Client, error) {
	specs := proxyclient.SpecsFromConfig(cfg)
	spec, _, err := proxyclient.Resolve(account, specs)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.Probe.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return proxyclient.HTTPClient(spec, timeout)
}

func fetchOpenAISubscriptionTier(ctx context.Context, client *http.Client, accessToken string) (string, error) {
	body, err := postJSONWithBearer(ctx, client, "https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:loadCodeAssist", accessToken, map[string]any{"metadata": map[string]any{"ideType": "ANTIGRAVITY"}})
	if err != nil {
		return "", err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	return normalizeSubscriptionTier(subscriptionTierFromPayload(payload)), nil
}

func fetchOpenAIUsageInfo(ctx context.Context, client *http.Client, accessToken string) (map[string]any, error) {
	body, err := postJSONWithBearer(ctx, client, "https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:fetchAvailableModels", accessToken, map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return usageInfoFromQuotaModels(payload), nil
}

func postJSONWithBearer(ctx context.Context, client *http.Client, url string, token string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Antigravity/1.0 simple-sub2api")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return responseBody, nil
}

func subscriptionTierFromPayload(payload map[string]any) string {
	for _, key := range []string{"paidTier", "currentTier"} {
		if tier := tierName(payload[key]); tier != "" {
			return tier
		}
	}
	if tiers, ok := payload["allowedTiers"].([]any); ok {
		for _, candidate := range tiers {
			item, ok := candidate.(map[string]any)
			if !ok {
				continue
			}
			if metadataBool(item, "isDefault") {
				return tierName(item)
			}
		}
	}
	return ""
}

func tierName(value any) string {
	item, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"name", "id", "slug", "quotaTier"} {
		if text, ok := item[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func usageInfoFromQuotaModels(payload map[string]any) map[string]any {
	models, ok := payload["models"].(map[string]any)
	if !ok || len(models) == 0 {
		return nil
	}
	lowestFiveHour := 100.0
	lowestSevenDay := 100.0
	var fiveHourReset string
	var sevenDayReset string
	for name, raw := range models {
		model, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		quotaInfo, ok := model["quotaInfo"].(map[string]any)
		if !ok {
			continue
		}
		percentage := firstFloatFromMaps([]map[string]any{quotaInfo}, []string{"remainingFraction"}) * 100
		if percentage <= 0 {
			continue
		}
		resetAt := firstMetadataString(quotaInfo, []string{"resetTime"}, "")
		lowerName := strings.ToLower(name)
		if strings.Contains(lowerName, "flash") || strings.Contains(lowerName, "5h") {
			if percentage < lowestFiveHour {
				lowestFiveHour = percentage
				fiveHourReset = resetAt
			}
			continue
		}
		if percentage < lowestSevenDay {
			lowestSevenDay = percentage
			sevenDayReset = resetAt
		}
	}
	usageInfo := map[string]any{}
	if lowestFiveHour < 100 {
		usageInfo["five_hour"] = remainingWindow("5h", lowestFiveHour, fiveHourReset)
	}
	if lowestSevenDay < 100 {
		usageInfo["seven_day"] = remainingWindow("7d", lowestSevenDay, sevenDayReset)
	}
	return usageInfo
}

func remainingWindow(label string, remainingPercent float64, resetAt string) map[string]any {
	usedPercent := 100 - remainingPercent
	if usedPercent < 0 {
		usedPercent = 0
	}
	window := map[string]any{"label": label, "utilization": usedPercent, "remaining_percent": remainingPercent}
	if resetAt != "" {
		window["reset_at"] = resetAt
		window["reset_time"] = resetAt
	}
	return window
}

func credentialValue(credential string, key string) string {
	for _, part := range strings.FieldsFunc(credential, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		name, value, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(name) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func exchangeOpenAIOAuthCode(ctx context.Context, client *http.Client, input openAIOAuthExchangeInput) (openAIOAuthTokenResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", input.ClientID)
	form.Set("code", input.Code)
	form.Set("redirect_uri", input.RedirectURI)
	form.Set("code_verifier", input.CodeVerifier)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, input.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return openAIOAuthTokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "codex-cli/0.91.0")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return openAIOAuthTokenResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return openAIOAuthTokenResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return openAIOAuthTokenResponse{}, fmt.Errorf("token exchange failed: status %d, body: %s", response.StatusCode, sanitizeCredentialText(string(body)))
	}
	var token openAIOAuthTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return openAIOAuthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return openAIOAuthTokenResponse{}, errors.New("token exchange response missing access_token")
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return openAIOAuthTokenResponse{}, errors.New("token exchange response missing refresh_token")
	}
	return token, nil
}

func sanitizeCredentialText(text string) string {
	for _, marker := range []string{"Bearer ", "sk-", "access_token=", "refresh_token=", "id_token=", "code=", "code_verifier="} {
		if strings.Contains(text, marker) {
			return "credential material redacted"
		}
	}
	return text
}

func sanitizeOpenAIOAuthExchangeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, marker := range []string{"Bearer ", "sk-", "access_token=", "refresh_token=", "id_token=", "code=", "code_verifier="} {
		if strings.Contains(message, marker) {
			return "OpenAI OAuth token exchange failed"
		}
	}
	return message
}

func joinRefreshMessages(healthMessage string, syncMessage string) string {
	if syncMessage == "" {
		return healthMessage
	}
	if healthMessage == "" {
		return syncMessage
	}
	return healthMessage + "; " + syncMessage
}

func sanitizeDisplaySyncError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, marker := range []string{"Bearer ", "sk-", "access_token=", "refresh_token="} {
		if strings.Contains(message, marker) {
			return "display sync failed"
		}
	}
	return message
}

func usageInfoFromQuotaAndMetadata(quotaState map[string]any, metadata map[string]any) map[string]any {
	fiveHourUsed := firstFloatFromMaps([]map[string]any{metadata, quotaState}, []string{"window_cost_used", "session_window_cost_used"})
	fiveHourLimit := firstFloatFromMaps([]map[string]any{metadata, quotaState}, []string{"window_cost_limit", "session_window_cost_limit"})
	weeklyUsed := firstFloatFromMaps([]map[string]any{quotaState, metadata}, []string{"weekly_used_tokens", "quota_weekly_used"})
	weeklyLimit := firstFloatFromMaps([]map[string]any{quotaState, metadata}, []string{"weekly_limit_tokens", "quota_weekly_limit"})
	usageInfo := map[string]any{}
	if fiveHourUsed > 0 || fiveHourLimit > 0 {
		usageInfo["five_hour"] = usageWindow("5h", fiveHourUsed, fiveHourLimit, firstMetadataString(metadata, []string{"session_window_reset_at", "window_cost_reset_at"}, ""))
	}
	if weeklyUsed > 0 || weeklyLimit > 0 {
		usageInfo["seven_day"] = usageWindow("7d", weeklyUsed, weeklyLimit, firstMetadataString(metadata, []string{"quota_weekly_reset_at", "weekly_reset_at"}, ""))
	}
	return usageInfo
}

func usageWindow(label string, used float64, limit float64, resetAt string) map[string]any {
	window := map[string]any{
		"label":       label,
		"used":        used,
		"limit":       limit,
		"utilization": usageUtilization(used, limit),
	}
	if resetAt != "" {
		window["reset_at"] = resetAt
		window["reset_time"] = resetAt
	}
	return window
}

func usageUtilization(used float64, limit float64) float64 {
	if used <= 0 || limit <= 0 {
		return 0
	}
	percent := used / limit * 100
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func mapFromMetadata(metadata map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := metadata[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func cloneDisplayMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func firstMetadataString(metadata map[string]any, keys []string, fallback string) string {
	for _, key := range keys {
		if value, ok := metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(fallback)
}

func metadataBool(metadata map[string]any, key string) bool {
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "enabled":
			return true
		}
	case float64:
		return value != 0
	case int:
		return value != 0
	}
	return false
}

func firstFloatFromMaps(maps []map[string]any, keys []string) float64 {
	for _, values := range maps {
		for _, key := range keys {
			switch value := values[key].(type) {
			case int:
				return float64(value)
			case int64:
				return float64(value)
			case float64:
				return value
			case json.Number:
				parsed, err := value.Float64()
				if err == nil {
					return parsed
				}
			case string:
				parsed, err := json.Number(strings.TrimSpace(value)).Float64()
				if err == nil {
					return parsed
				}
			}
		}
	}
	return 0
}

func normalizeSubscriptionTier(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "ultra"):
		return "ultra"
	case strings.Contains(lower, "pro") || strings.Contains(lower, "paid") || strings.Contains(lower, "standard") || strings.Contains(lower, "enterprise") || strings.Contains(lower, "team") || strings.Contains(lower, "plus"):
		return "pro"
	default:
		return "free"
	}
}

func (s *Server) keysResponse() keysResponse {
	snapshot := s.store.Snapshot()
	cfg := config.Redacted(snapshot)
	keys := make([]config.GatewayKey, 0, len(cfg.GatewayKeys))
	for _, key := range snapshot.GatewayKeys {
		redacted := redactedGatewayKey(key)
		if strings.TrimSpace(key.KeyValue) != "" && key.KeyHash == config.HashGatewayKey(key.KeyValue) {
			redacted.KeyValue = key.KeyValue
		} else if key.ID == "default" && keyMatchesGatewayAuth(snapshot, key) {
			redacted.KeyValue = snapshot.GatewayAuth.GatewayKey
		}
		keys = append(keys, redacted)
	}
	return keysResponse{ConfigVersion: cfg.ConfigVersion, Keys: keys, Accounts: cfg.Accounts, Groups: cfg.Groups}
}

func (s *Server) groupsResponse() groupsResponse {
	cfg := config.Redacted(s.store.Snapshot())
	summaries := make([]groupSummary, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		summaries = append(summaries, groupSummary{Config: group, AccountCount: len(group.AccountIDs)})
	}
	return groupsResponse{ConfigVersion: cfg.ConfigVersion, Groups: summaries, Accounts: cfg.Accounts}
}

func (s *Server) writeUpdateResult(w http.ResponseWriter, cfg config.Config, err error) {
	if err != nil {
		if errors.Is(err, config.ErrConfigVersionConflict) {
			http.Error(w, "config_version conflict", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, config.Redacted(cfg))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return false
	}
	return true
}

func upsertOAuthSource(sources []config.OAuthSource, source config.OAuthSource) []config.OAuthSource {
	out := append([]config.OAuthSource(nil), sources...)
	for i := range out {
		if out[i].ID == source.ID {
			out[i] = source
			return out
		}
	}
	return append(out, source)
}

func deleteOAuthSource(sources []config.OAuthSource, id string) []config.OAuthSource {
	out := make([]config.OAuthSource, 0, len(sources))
	for _, source := range sources {
		if source.ID != id {
			out = append(out, source)
		}
	}
	return out
}

func upsertSubscriptionSource(sources []config.SubscriptionSource, source config.SubscriptionSource) []config.SubscriptionSource {
	out := append([]config.SubscriptionSource(nil), sources...)
	for i := range out {
		if out[i].ID == source.ID {
			out[i] = source
			return out
		}
	}
	return append(out, source)
}

func deleteSubscriptionSource(sources []config.SubscriptionSource, id string) []config.SubscriptionSource {
	out := make([]config.SubscriptionSource, 0, len(sources))
	for _, source := range sources {
		if source.ID != id {
			out = append(out, source)
		}
	}
	return out
}

func deleteAccount(accounts []config.Account, id string) []config.Account {
	out := make([]config.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.ID != id {
			out = append(out, account)
		}
	}
	return out
}

func removeAccountFromGatewayPolicies(keys []config.GatewayKey, id string) {
	for i := range keys {
		keys[i].RoutingPolicy.AccountIDs = removeString(keys[i].RoutingPolicy.AccountIDs, id)
	}
}

func removeAccountFromGroups(groups []config.Group, id string) {
	for i := range groups {
		groups[i].AccountIDs = removeString(groups[i].AccountIDs, id)
	}
}

func removeGroupFromGatewayPolicies(keys []config.GatewayKey, id string) {
	for i := range keys {
		keys[i].RoutingPolicy.GroupIDs = removeString(keys[i].RoutingPolicy.GroupIDs, id)
	}
}

func removeString(values []string, value string) []string {
	out := make([]string, 0, len(values))
	for _, candidate := range values {
		if candidate != value {
			out = append(out, candidate)
		}
	}
	return out
}

func upsertGatewayKey(keys []config.GatewayKey, key config.GatewayKey) []config.GatewayKey {
	out := append([]config.GatewayKey(nil), keys...)
	for i := range out {
		if out[i].ID == key.ID {
			out[i] = key
			return out
		}
	}
	return append(out, key)
}

func deleteGatewayKey(keys []config.GatewayKey, id string) []config.GatewayKey {
	out := make([]config.GatewayKey, 0, len(keys))
	for _, key := range keys {
		if key.ID != id {
			out = append(out, key)
		}
	}
	return out
}

func gatewayKeyExists(keys []config.GatewayKey, id string) bool {
	for _, key := range keys {
		if key.ID == id {
			return true
		}
	}
	return false
}

func upsertGroup(groups []config.Group, group config.Group) []config.Group {
	out := append([]config.Group(nil), groups...)
	for i := range out {
		if out[i].ID == group.ID {
			out[i] = group
			return out
		}
	}
	return append(out, group)
}

func deleteGroup(groups []config.Group, id string) []config.Group {
	out := make([]config.Group, 0, len(groups))
	for _, group := range groups {
		if group.ID != id {
			out = append(out, group)
		}
	}
	return out
}

func groupExists(groups []config.Group, id string) bool {
	for _, group := range groups {
		if group.ID == id {
			return true
		}
	}
	return false
}

func redactedGatewayKey(key config.GatewayKey) config.GatewayKey {
	key.KeyHash = "configured"
	key.KeyValue = ""
	return key
}

func keyMatchesGatewayAuth(cfg config.Config, key config.GatewayKey) bool {
	plaintext := strings.TrimSpace(cfg.GatewayAuth.GatewayKey)
	if plaintext == "" || strings.TrimSpace(key.KeyHash) == "" {
		return false
	}
	return key.KeyHash == config.HashGatewayKey(plaintext) && key.Preview == config.KeyPreview(plaintext)
}

func gatewayKeyIDFromName(name string, now string) string {
	return idFromName(name, now, "gateway_key")
}

func groupIDFromName(name string, now string) string {
	return idFromName(name, now, "group")
}

func accountIDFromName(name string, now string) string {
	return "acct_" + idFromName(name, now, "account")
}

func idFromName(name string, now string, fallback string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	if base == "" {
		base = fallback
	}
	var b strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == '_' || r == '-' || r == '.' || r == ' ' {
			b.WriteByte('_')
		}
	}
	base = strings.Trim(b.String(), "_-. ")
	if len(base) < 2 {
		base = fallback
	}
	if len(base) > 40 {
		base = base[:40]
	}
	suffix := strings.NewReplacer("-", "", ":", "", "T", "", "Z", "").Replace(now)
	if len(suffix) > 14 {
		suffix = suffix[:14]
	}
	return base + "_" + suffix
}

func ensureString(values []string, value string) []string {
	for _, candidate := range values {
		if candidate == value {
			return values
		}
	}
	return append(values, value)
}

func mergeUnique(values []string, extra []string) []string {
	out := append([]string(nil), values...)
	for _, value := range extra {
		out = ensureString(out, value)
	}
	return out
}

func groupIDsForAccount(accountIDs []string, groups []config.Group) []string {
	out := []string{}
	for _, accountID := range accountIDs {
		for _, group := range groups {
			for _, candidate := range group.AccountIDs {
				if candidate == accountID {
					out = ensureString(out, group.ID)
				}
			}
		}
	}
	return out
}

func upsertAccount(accounts []config.Account, account config.Account) []config.Account {
	out := append([]config.Account(nil), accounts...)
	for i := range out {
		if out[i].ID == account.ID {
			out[i] = account
			return out
		}
	}
	return append(out, account)
}

func accountExists(accounts []config.Account, id string) bool {
	for _, account := range accounts {
		if account.ID == id {
			return true
		}
	}
	return false
}

func preserveRedactedAccountSecret(candidate *config.Account, current []config.Account) {
	if !isMasked(candidate.Credential) {
		return
	}
	for _, account := range current {
		if account.ID == candidate.ID {
			candidate.Credential = account.Credential
			return
		}
	}
}

func countEnabledAccounts(accounts []config.Account) int {
	count := 0
	for _, account := range accounts {
		if account.Enabled {
			count++
		}
	}
	return count
}

func preserveRedactedSecrets(candidate *config.Config, current config.Config) {
	if isMasked(candidate.GatewayAuth.GatewayKey) {
		candidate.GatewayAuth.GatewayKey = current.GatewayAuth.GatewayKey
	}
	if candidate.Dashboard.AdminPassword == "configured" {
		candidate.Dashboard.AdminPassword = current.Dashboard.AdminPassword
	}
	currentAccounts := map[string]config.Account{}
	for _, account := range current.Accounts {
		currentAccounts[account.ID] = account
	}
	for i := range candidate.Accounts {
		if isMasked(candidate.Accounts[i].Credential) {
			candidate.Accounts[i].Credential = currentAccounts[candidate.Accounts[i].ID].Credential
		}
	}
	currentOAuth := map[string]config.OAuthSource{}
	for _, source := range current.OAuthSources {
		currentOAuth[source.ID] = source
	}
	for i := range candidate.OAuthSources {
		for key, value := range candidate.OAuthSources[i].Credentials {
			if isMasked(value) {
				candidate.OAuthSources[i].Credentials[key] = currentOAuth[candidate.OAuthSources[i].ID].Credentials[key]
			}
		}
	}
	currentSubs := map[string]config.SubscriptionSource{}
	for _, source := range current.SubscriptionSources {
		currentSubs[source.ID] = source
	}
	for i := range candidate.SubscriptionSources {
		if isMasked(candidate.SubscriptionSources[i].InlineBundle) {
			candidate.SubscriptionSources[i].InlineBundle = currentSubs[candidate.SubscriptionSources[i].ID].InlineBundle
		}
	}
	if isMasked(candidate.Tunnel.Token) {
		candidate.Tunnel.Token = current.Tunnel.Token
	}
}

func isMasked(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || trimmed == "redacted" || strings.Contains(trimmed, "...")
}

func upsertProxy(proxies []config.ProxyConfig, proxy config.ProxyConfig) []config.ProxyConfig {
	out := append([]config.ProxyConfig(nil), proxies...)
	for i := range out {
		if out[i].ID == proxy.ID {
			out[i] = proxy
			return out
		}
	}
	return append(out, proxy)
}

func deleteProxy(proxies []config.ProxyConfig, id string) []config.ProxyConfig {
	out := make([]config.ProxyConfig, 0, len(proxies))
	for _, proxy := range proxies {
		if proxy.ID != id {
			out = append(out, proxy)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
