package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/accountcheck"
	"github.com/0xForce-Network/simple-sub2api/internal/accountpool"
	"github.com/0xForce-Network/simple-sub2api/internal/config"
	"github.com/0xForce-Network/simple-sub2api/internal/dashboard"
	"github.com/0xForce-Network/simple-sub2api/internal/gateway"
	"github.com/0xForce-Network/simple-sub2api/internal/gatewayauth"
	"github.com/0xForce-Network/simple-sub2api/internal/metrics"
	"github.com/0xForce-Network/simple-sub2api/internal/routing"
	subscriptionimport "github.com/0xForce-Network/simple-sub2api/internal/subscription_import"
	"github.com/0xForce-Network/simple-sub2api/internal/version"
)

type Server struct {
	store    *config.Store
	logger   *slog.Logger
	sessions *dashboard.SessionManager
	pool     *accountpool.Manager
	metrics  *metrics.Recorder
}

func New(store *config.Store, logger *slog.Logger) *Server {
	cfg := store.Snapshot()
	pool, err := accountpool.NewManager(cfg)
	if err != nil && logger != nil {
		logger.Error("account pool init failed", "error", err)
	}
	return &Server{
		store:    store,
		logger:   logger,
		sessions: dashboard.NewSessionManager(time.Duration(cfg.Dashboard.SessionTTLSeconds) * time.Second),
		pool:     pool,
		metrics:  metrics.NewRecorder(cfg.Metrics.RecentErrorsLimit),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/version", s.version)
	mux.HandleFunc("/", s.dashboardIndex)
	mux.HandleFunc("/dashboard", s.dashboardIndex)
	mux.Handle("/v1/", gatewayauth.Authorizer{Provider: s.store}.Middleware(http.HandlerFunc(s.v1Gateway)))
	mux.HandleFunc("/api/admin/login", s.login)
	mux.Handle("/api/admin/logout", s.adminOnly(http.HandlerFunc(s.logout)))
	mux.Handle("/api/admin/me", s.adminOnly(http.HandlerFunc(s.me)))
	mux.Handle("/api/admin/dashboard/state", s.adminOnly(http.HandlerFunc(s.dashboardState)))
	mux.Handle("/api/admin/gateway-key", s.adminOnly(http.HandlerFunc(s.revealGatewayKey)))
	mux.Handle("/api/admin/gateway-key/rotate", s.adminOnly(http.HandlerFunc(s.rotateGatewayKey)))
	mux.Handle("/api/admin/config", s.adminOnly(http.HandlerFunc(s.configState)))
	mux.Handle("/api/admin/config/save", s.adminOnly(http.HandlerFunc(s.configSave)))
	mux.Handle("/api/admin/accounts/", s.adminOnly(http.HandlerFunc(s.accountByID)))
	mux.Handle("/api/admin/oauth-sources", s.adminOnly(http.HandlerFunc(s.oauthSources)))
	mux.Handle("/api/admin/oauth-sources/", s.adminOnly(http.HandlerFunc(s.oauthSourceByID)))
	mux.Handle("/api/admin/subscription-sources", s.adminOnly(http.HandlerFunc(s.subscriptionSources)))
	mux.Handle("/api/admin/subscription-sources/", s.adminOnly(http.HandlerFunc(s.subscriptionSourceByID)))
	mux.Handle("/api/admin/import/preview", s.adminOnly(http.HandlerFunc(s.importPreview)))
	mux.Handle("/api/admin/import/apply", s.adminOnly(http.HandlerFunc(s.importApply)))
	mux.Handle("/api/admin/proxies", s.adminOnly(http.HandlerFunc(s.proxies)))
	mux.Handle("/api/admin/proxies/", s.adminOnly(http.HandlerFunc(s.proxyByID)))
	mux.Handle("/api/admin/quota", s.adminOnly(http.HandlerFunc(s.quotaConfig)))
	mux.Handle("/api/admin/quota/state", s.adminOnly(http.HandlerFunc(s.quotaState)))
	mux.Handle("/api/admin/routing", s.adminOnly(http.HandlerFunc(s.routingConfig)))
	mux.Handle("/api/admin/routing/decide", s.adminOnly(http.HandlerFunc(s.routingDecide)))
	mux.Handle("/api/admin/account-check", s.adminOnly(http.HandlerFunc(s.accountCheck)))
	mux.Handle("/api/admin/account-pool", s.adminOnly(http.HandlerFunc(s.accountPool)))
	mux.Handle("/api/admin/metrics", s.adminOnly(http.HandlerFunc(s.metricsSnapshot)))
	return s.cors(mux)
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
	if r.URL.Path != "/" && r.URL.Path != "/dashboard" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
		"gateway":      map[string]any{"key_configured": s.store.GatewayKey() != ""},
	})
}

func (s *Server) revealGatewayKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

func (s *Server) accountByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/accounts/")
	if strings.TrimSpace(path) == "" {
		http.Error(w, "account id required", http.StatusBadRequest)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id := parts[0]
	if len(parts) == 2 && parts[1] == "refresh" {
		s.accountRefresh(w, r, id)
		return
	}
	if len(parts) != 1 || r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req versionedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := s.updateConfig(req.ConfigVersion, func(cfg *config.Config) error {
		cfg.Accounts = deleteAccount(cfg.Accounts, id)
		return nil
	})
	s.writeUpdateResult(w, updated, err)
}

func (s *Server) accountRefresh(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := s.store.Snapshot()
	for _, account := range cfg.Accounts {
		if account.ID == id {
			result := accountcheck.New(cfg.Probe).Check(r.Context(), cfg, account)
			if s.pool != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Probe.TimeoutSeconds)*time.Second)
				defer cancel()
				_ = s.pool.ApplyConfig(ctx, cfg)
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
	}
	http.Error(w, "account not found", http.StatusNotFound)
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
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	return updated, nil
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

type sourceMutationRequest[T any] struct {
	ConfigVersion int `json:"config_version"`
	Source        T   `json:"source"`
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
