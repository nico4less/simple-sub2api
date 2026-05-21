package gatewayauth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type KeyProvider interface {
	GatewayKey() string
}

type Authorizer struct {
	Provider KeyProvider
}

func (a Authorizer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := a.Provider.GatewayKey()
		if key == "" || !matchesBearer(r.Header.Get("Authorization"), key) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "gateway key required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func matchesBearer(header string, key string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	candidate := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if len(candidate) != len(key) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(key)) == 1
}
