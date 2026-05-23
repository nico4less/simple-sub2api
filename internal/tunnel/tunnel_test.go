package tunnel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

func TestExtractPublicURL(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "quick tunnel", line: "INF | https://random-link.trycloudflare.com | ready", want: "https://random-link.trycloudflare.com"},
		{name: "named hostname escaped", line: `Updated to new configuration config="{\"ingress\":[{\"hostname\":\"api.example.com\"}]}"`, want: "https://api.example.com"},
		{name: "none", line: "Registered tunnel connection", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractPublicURL(tt.line); got != tt.want {
				t.Fatalf("ExtractPublicURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManagerObservesLogsAndRingBuffer(t *testing.T) {
	m := NewManager()
	m.logLimitLines = 3
	m.observeLogLine("line 1")
	m.observeLogLine("line 2")
	m.observeLogLine("line 3")
	m.observeLogLine("INF https://abc-123.trycloudflare.com ready")
	status := m.Status()
	if status.Status != StatusConnected {
		t.Fatalf("status = %q, want connected", status.Status)
	}
	if status.PublicURL != "https://abc-123.trycloudflare.com" {
		t.Fatalf("public URL = %q", status.PublicURL)
	}
	if status.ActiveSince == "" {
		t.Fatal("active_since was not set")
	}
	if len(status.RecentLogs) != 3 {
		t.Fatalf("recent log count = %d", len(status.RecentLogs))
	}
	if status.RecentLogs[0] != "line 2" || !strings.Contains(status.RecentLogs[2], "trycloudflare") {
		t.Fatalf("ring buffer logs = %#v", status.RecentLogs)
	}

	m.observeLogLine("Registered tunnel connection")
	status = m.Status()
	if status.Status != StatusConnected {
		t.Fatalf("named connected marker changed status to %q", status.Status)
	}
}

func TestBuildArgsAndLocalTargetURL(t *testing.T) {
	target, err := localTargetURL("0.0.0.0:1455")
	if err != nil {
		t.Fatalf("localTargetURL() error = %v", err)
	}
	if target != "http://127.0.0.1:1455" {
		t.Fatalf("target = %q", target)
	}
	quick := buildArgs(config.TunnelConfig{Mode: "quick"}, target)
	if strings.Join(quick, " ") != "tunnel --url http://127.0.0.1:1455" {
		t.Fatalf("quick args = %#v", quick)
	}
	named := buildArgs(config.TunnelConfig{Mode: "named", Token: "secret-token"}, target)
	if strings.Join(named, " ") != "tunnel run --token secret-token" {
		t.Fatalf("named args = %#v", named)
	}
	if strings.Contains(safeArgs(named), "secret-token") {
		t.Fatalf("safeArgs leaked token: %q", safeArgs(named))
	}
}

func TestStartMissingBinarySetsError(t *testing.T) {
	m := NewManager()
	err := m.Start(config.TunnelConfig{Enabled: true, Mode: "quick", BinaryPath: filepath.Join(t.TempDir(), "missing-cloudflared"), LogLimitLines: 10}, "127.0.0.1:8080")
	if err == nil {
		t.Fatal("Start() accepted missing binary")
	}
	status := m.Status()
	if status.Status != StatusError || !strings.Contains(status.ErrorMessage, "cloudflared binary not found") {
		t.Fatalf("status = %#v", status)
	}
}

func TestManagerStartsParsesURLAndStops(t *testing.T) {
	script := writeMockCloudflared(t, `#!/bin/sh
echo "INF starting quick tunnel" >&2
echo "INF https://mock-url.trycloudflare.com ready" >&2
sleep 30
`)
	m := NewManager(WithBackoff(10*time.Millisecond, 10*time.Millisecond))
	if err := m.Start(config.TunnelConfig{Enabled: true, Mode: "quick", BinaryPath: script, LogLimitLines: 10}, "127.0.0.1:18080"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return m.Status().Status == StatusConnected
	})
	status := m.Status()
	if status.PublicURL != "https://mock-url.trycloudflare.com" {
		t.Fatalf("status = %#v", status)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := m.Status().Status; got != StatusStopped {
		t.Fatalf("status after stop = %q", got)
	}
}

func TestManagerSupervisorRestartsExitedProcess(t *testing.T) {
	script := writeMockCloudflared(t, `#!/bin/sh
echo "INF https://restart-check.trycloudflare.com ready" >&2
exit 2
`)
	var starts atomic.Int32
	m := NewManager(
		WithBackoff(10*time.Millisecond, 10*time.Millisecond),
		WithCommandFactory(func(ctx context.Context, binary string, args ...string) *exec.Cmd {
			starts.Add(1)
			return exec.CommandContext(ctx, binary, args...)
		}),
	)
	if err := m.Start(config.TunnelConfig{Enabled: true, Mode: "quick", BinaryPath: script, LogLimitLines: 20}, "127.0.0.1:18080"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return starts.Load() >= 2
	})
	if starts.Load() < 2 {
		t.Fatalf("starts = %d, want restart", starts.Load())
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func writeMockCloudflared(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell mock is unix-only")
	}
	path := filepath.Join(t.TempDir(), "cloudflared-mock.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write mock cloudflared: %v", err)
	}
	return path
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("condition not met within %s", timeout))
}
