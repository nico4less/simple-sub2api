package dashboard

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

const CookieName = "simple_sub2api_admin_session"

type session struct {
	ExpiresAt time.Time
}

type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]session
	ttl      time.Duration
}

func NewSessionManager(ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &SessionManager{sessions: make(map[string]session), ttl: ttl}
}

func (m *SessionManager) Issue(w http.ResponseWriter) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(m.ttl)
	m.mu.Lock()
	m.sessions[token] = session{ExpiresAt: expires}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(m.ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (m *SessionManager) Authenticate(r *http.Request) bool {
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.sessions[cookie.Value]
	if !ok {
		return false
	}
	if now.After(record.ExpiresAt) {
		delete(m.sessions, cookie.Value)
		return false
	}
	return true
}

func (m *SessionManager) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(CookieName); err == nil {
		m.mu.Lock()
		delete(m.sessions, cookie.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
