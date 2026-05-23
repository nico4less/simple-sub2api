package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

type Status string

const (
	StatusStopped   Status = "stopped"
	StatusStarting  Status = "starting"
	StatusConnected Status = "connected"
	StatusError     Status = "error"
)

const (
	defaultLogLimitLines  = 100
	defaultInitialBackoff = time.Second
	defaultMaxBackoff     = 60 * time.Second
)

var (
	tryCloudflareURLPattern = regexp.MustCompile(`https://[A-Za-z0-9-]+\.trycloudflare\.com`)
	namedHostnamePattern    = regexp.MustCompile(`\\?"hostname\\?"\s*:\s*\\?"([^"\\]+)\\?"`)
)

type RuntimeStatus struct {
	Status       Status   `json:"status"`
	PublicURL    string   `json:"public_url"`
	ErrorMessage string   `json:"error_message,omitempty"`
	ActiveSince  string   `json:"active_since,omitempty"`
	RecentLogs   []string `json:"recent_logs"`
}

type CommandFactory func(ctx context.Context, binary string, args ...string) *exec.Cmd

type Option func(*Manager)

type Manager struct {
	mu sync.RWMutex

	cmd              *exec.Cmd
	status           RuntimeStatus
	logLines         []string
	logLimitLines    int
	supervisorCancel context.CancelFunc
	supervisorDone   chan struct{}

	commandFactory CommandFactory
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

type Controller interface {
	Apply(cfg config.TunnelConfig, bind string) error
	Status() RuntimeStatus
	Stop() error
}

func NewManager(opts ...Option) *Manager {
	m := &Manager{
		status:           RuntimeStatus{Status: StatusStopped},
		logLimitLines:    defaultLogLimitLines,
		commandFactory:   exec.CommandContext,
		initialBackoff:   defaultInitialBackoff,
		maxBackoff:       defaultMaxBackoff,
		supervisorCancel: nil,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func WithCommandFactory(factory CommandFactory) Option {
	return func(m *Manager) {
		if factory != nil {
			m.commandFactory = factory
		}
	}
}

func WithBackoff(initial time.Duration, max time.Duration) Option {
	return func(m *Manager) {
		if initial > 0 {
			m.initialBackoff = initial
		}
		if max > 0 {
			m.maxBackoff = max
		}
		if m.maxBackoff < m.initialBackoff {
			m.maxBackoff = m.initialBackoff
		}
	}
}

func (m *Manager) Apply(cfg config.TunnelConfig, bind string) error {
	if !cfg.Enabled {
		return m.Stop()
	}
	return m.Start(cfg, bind)
}

func (m *Manager) Start(cfg config.TunnelConfig, bind string) error {
	cfg = normalizeConfig(cfg)
	if !cfg.Enabled {
		return m.Stop()
	}
	if cfg.Mode == "named" && strings.TrimSpace(cfg.Token) == "" {
		return errors.New("tunnel token is required for named mode")
	}
	targetURL, err := localTargetURL(bind)
	if err != nil {
		m.setError(err.Error())
		return err
	}
	binary, err := resolveBinary(cfg.BinaryPath)
	if err != nil {
		m.setError(err.Error())
		return err
	}
	if err := m.Stop(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	m.logLimitLines = cfg.LogLimitLines
	m.logLines = nil
	m.status = RuntimeStatus{Status: StatusStarting}
	m.supervisorCancel = cancel
	m.supervisorDone = done
	m.mu.Unlock()
	go m.supervise(ctx, done, cfg, binary, targetURL)
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	cancel := m.supervisorCancel
	done := m.supervisorDone
	cmd := m.cmd
	m.supervisorCancel = nil
	m.supervisorDone = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		_ = terminateProcess(cmd)
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}
	m.mu.Lock()
	m.cmd = nil
	m.status.Status = StatusStopped
	m.status.PublicURL = ""
	m.status.ErrorMessage = ""
	m.status.ActiveSince = ""
	m.mu.Unlock()
	return nil
}

func (m *Manager) Status() RuntimeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := m.status
	status.RecentLogs = append([]string(nil), m.logLines...)
	return status
}

func (m *Manager) supervise(ctx context.Context, done chan struct{}, cfg config.TunnelConfig, binary string, targetURL string) {
	defer close(done)
	backoff := m.initialBackoff
	for {
		err := m.runTunnelProcess(ctx, cfg, binary, targetURL)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.setError(err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < m.maxBackoff {
			backoff *= 2
			if backoff > m.maxBackoff {
				backoff = m.maxBackoff
			}
		}
	}
}

func (m *Manager) runTunnelProcess(ctx context.Context, cfg config.TunnelConfig, binary string, targetURL string) error {
	args := buildArgs(cfg, targetURL)
	m.appendLog(fmt.Sprintf("starting cloudflared %s", safeArgs(args)))
	cmd := m.commandFactory(ctx, binary, args...)
	configureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create cloudflared stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("create cloudflared stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cloudflared: %w", err)
	}
	m.mu.Lock()
	m.cmd = cmd
	m.status.Status = StatusStarting
	m.status.ErrorMessage = ""
	m.status.PublicURL = ""
	m.status.ActiveSince = ""
	m.mu.Unlock()
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		m.scanLogs(stdout)
	}()
	go func() {
		defer readers.Done()
		m.scanLogs(stderr)
	}()
	err = cmd.Wait()
	readers.Wait()
	m.mu.Lock()
	if m.cmd == cmd {
		m.cmd = nil
	}
	m.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cloudflared exited: %w", err)
	}
	return errors.New("cloudflared exited unexpectedly")
}

func (m *Manager) scanLogs(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		m.observeLogLine(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		m.appendLog(fmt.Sprintf("log scanner error: %v", err))
	}
}

func (m *Manager) observeLogLine(line string) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return
	}
	m.appendLog(line)
	if url := ExtractPublicURL(line); url != "" {
		m.markConnected(url)
		return
	}
	if IsNamedTunnelConnectedLog(line) {
		m.markConnected("")
	}
}

func (m *Manager) appendLog(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := m.logLimitLines
	if limit <= 0 {
		limit = defaultLogLimitLines
	}
	m.logLines = append(m.logLines, line)
	if len(m.logLines) > limit {
		m.logLines = append([]string(nil), m.logLines[len(m.logLines)-limit:]...)
	}
}

func (m *Manager) markConnected(publicURL string) {
	now := time.Now().UTC().Format(time.RFC3339)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Status = StatusConnected
	if publicURL != "" {
		m.status.PublicURL = publicURL
	}
	m.status.ErrorMessage = ""
	if m.status.ActiveSince == "" {
		m.status.ActiveSince = now
	}
}

func (m *Manager) setError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Status = StatusError
	m.status.ErrorMessage = message
	m.status.PublicURL = ""
	m.status.ActiveSince = ""
}

func ExtractPublicURL(line string) string {
	if match := tryCloudflareURLPattern.FindString(line); match != "" {
		return match
	}
	if strings.Contains(line, "hostname") {
		matches := namedHostnamePattern.FindStringSubmatch(line)
		if len(matches) == 2 && strings.TrimSpace(matches[1]) != "" {
			return "https://" + matches[1]
		}
	}
	return ""
}

func IsNamedTunnelConnectedLog(line string) bool {
	for _, marker := range []string{"Registered tunnel connection", "Route registered", "Connection registered"} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

func normalizeConfig(cfg config.TunnelConfig) config.TunnelConfig {
	cfg.Mode = strings.TrimSpace(cfg.Mode)
	if cfg.Mode == "" {
		cfg.Mode = "quick"
	}
	cfg.BinaryPath = strings.TrimSpace(cfg.BinaryPath)
	cfg.Token = strings.TrimSpace(cfg.Token)
	if cfg.LogLimitLines <= 0 {
		cfg.LogLimitLines = defaultLogLimitLines
	}
	return cfg
}

func resolveBinary(binaryPath string) (string, error) {
	if strings.TrimSpace(binaryPath) != "" {
		candidate := strings.TrimSpace(binaryPath)
		info, err := os.Stat(candidate)
		if err != nil {
			return "", fmt.Errorf("cloudflared binary not found at %q: %w", candidate, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("cloudflared binary path %q is a directory", candidate)
		}
		return candidate, nil
	}
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		return "", fmt.Errorf("cloudflared binary not found: %w", err)
	}
	return path, nil
}

func localTargetURL(bind string) (string, error) {
	_, port, err := net.SplitHostPort(bind)
	if err != nil {
		return "", fmt.Errorf("parse server bind for tunnel target: %w", err)
	}
	if strings.TrimSpace(port) == "" {
		return "", errors.New("server bind port is required for tunnel target")
	}
	return "http://127.0.0.1:" + port, nil
}

func buildArgs(cfg config.TunnelConfig, targetURL string) []string {
	if cfg.Mode == "named" {
		return []string{"tunnel", "run", "--token", cfg.Token}
	}
	return []string{"tunnel", "--url", targetURL}
}

func safeArgs(args []string) string {
	safe := append([]string(nil), args...)
	for i := 0; i < len(safe)-1; i++ {
		if safe[i] == "--token" {
			safe[i+1] = "[redacted]"
		}
	}
	return strings.Join(safe, " ")
}
