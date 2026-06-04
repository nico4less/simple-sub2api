package gatewayauth

import (
	"context"
	"net/http"
	"strings"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

type KeyProvider interface {
	MatchGatewayKey(value string) config.GatewayKeyMatch
}

type contextKey string

const matchedGatewayKeyContextKey contextKey = "matched_gateway_key"

type Authorizer struct {
	Provider KeyProvider
}

func (a Authorizer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate, ok := requestGatewayKey(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "gateway key required", http.StatusUnauthorized)
			return
		}
		match := a.Provider.MatchGatewayKey(candidate)
		if !match.OK {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "gateway key required", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), matchedGatewayKeyContextKey, match.Key)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func MatchedGatewayKey(r *http.Request) (config.GatewayKey, bool) {
	key, ok := r.Context().Value(matchedGatewayKeyContextKey).(config.GatewayKey)
	return key, ok
}

func requestGatewayKey(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	if candidate, ok := bearerToken(r.Header.Get("Authorization")); ok {
		return candidate, true
	}
	return apiKeyHeader(r.Header.Get("X-Api-Key"))
}

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	candidate := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return candidate, candidate != ""
}

func apiKeyHeader(header string) (string, bool) {
	candidate := strings.TrimSpace(header)
	return candidate, candidate != ""
}
