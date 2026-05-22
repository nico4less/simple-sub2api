package accountcheck_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/accountcheck"
	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

func TestCheckModelsNormalizesOriginAndV1BaseURL(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
	}{
		{name: "origin", basePath: ""},
		{name: "v1 base", basePath: "/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				if got := r.Header.Get("Authorization"); got != "Bearer sk-upstream" {
					t.Fatalf("Authorization = %q", got)
				}
				_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			}))
			defer upstream.Close()

			cfg := config.DefaultConfig()
			checker := accountcheck.Checker{Timeout: time.Second}
			result := checker.Check(context.Background(), cfg, config.Account{ID: "acct_1", Credential: "api_key=sk-upstream", BaseURL: upstream.URL + tt.basePath, Enabled: true})
			if result.Status != "healthy" {
				t.Fatalf("Check() result = %#v", result)
			}
			if gotPath != "/v1/models" {
				t.Fatalf("probe path = %q, want /v1/models", gotPath)
			}
		})
	}
}
