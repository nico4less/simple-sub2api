package proxyclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/0xForce-Network/simple-sub2api/internal/config"
)

type Spec struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func SpecsFromConfig(cfg config.Config) map[string]Spec {
	out := make(map[string]Spec, len(cfg.Proxies))
	for _, proxy := range cfg.Proxies {
		out[proxy.ID] = Spec{ID: proxy.ID, URL: config.NormalizeProxyURL(proxy.URL)}
	}
	return out
}

func Resolve(account config.Account, specs map[string]Spec) (Spec, bool, error) {
	if strings.TrimSpace(account.ProxyRef) == "" {
		return Spec{}, false, nil
	}
	spec, ok := specs[account.ProxyRef]
	if !ok {
		return Spec{}, true, fmt.Errorf("account %q references unknown proxy", account.ID)
	}
	if err := ValidateURL(spec.URL); err != nil {
		return Spec{}, true, err
	}
	return spec, true, nil
}

func ValidateURL(raw string) error {
	parsed, err := url.Parse(config.NormalizeProxyURL(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("proxy URL must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5h" {
		return fmt.Errorf("proxy scheme %q is not supported", parsed.Scheme)
	}
	return nil
}

func HTTPClient(spec Spec, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(spec.URL) != "" {
		parsed, err := url.Parse(config.NormalizeProxyURL(spec.URL))
		if err != nil {
			return nil, err
		}
		switch parsed.Scheme {
		case "http", "https":
			transport.Proxy = http.ProxyURL(parsed)
		case "socks5h":
			transport.Proxy = nil
			transport.DialContext = socks5HDialContext(parsed)
		default:
			return nil, fmt.Errorf("proxy scheme %q is not supported", parsed.Scheme)
		}
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

func socks5HDialContext(proxyURL *url.URL) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("socks5h supports tcp only, got %s", network)
		}
		dialer := &net.Dialer{}
		conn, err := dialer.DialContext(ctx, "tcp", proxyURL.Host)
		if err != nil {
			return nil, fmt.Errorf("proxy dial failed: %w", err)
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
			defer conn.SetDeadline(time.Time{})
		}
		if err := socks5Handshake(conn, proxyURL, address); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	}
}

func socks5Handshake(conn net.Conn, proxyURL *url.URL, address string) error {
	methods := []byte{0x00}
	username := ""
	password := ""
	if proxyURL.User != nil {
		username = proxyURL.User.Username()
		password, _ = proxyURL.User.Password()
		methods = append(methods, 0x02)
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return fmt.Errorf("proxy auth negotiation failed: %w", err)
	}
	choice := make([]byte, 2)
	if _, err := io.ReadFull(conn, choice); err != nil {
		return fmt.Errorf("proxy auth negotiation read failed: %w", err)
	}
	if choice[0] != 0x05 {
		return errors.New("proxy is not socks5")
	}
	if choice[1] == 0xff {
		return errors.New("proxy rejected all auth methods")
	}
	if choice[1] == 0x02 {
		if len(username) > 255 || len(password) > 255 {
			return errors.New("proxy username/password too long")
		}
		packet := []byte{0x01, byte(len(username))}
		packet = append(packet, []byte(username)...)
		packet = append(packet, byte(len(password)))
		packet = append(packet, []byte(password)...)
		if _, err := conn.Write(packet); err != nil {
			return fmt.Errorf("proxy username/password auth failed: %w", err)
		}
		result := make([]byte, 2)
		if _, err := io.ReadFull(conn, result); err != nil {
			return fmt.Errorf("proxy username/password auth read failed: %w", err)
		}
		if result[1] != 0x00 {
			return errors.New("proxy username/password auth rejected")
		}
	} else if choice[1] != 0x00 {
		return fmt.Errorf("proxy selected unsupported auth method %d", choice[1])
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("target address must be host:port: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("target port is invalid")
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 0x01)
			request = append(request, ip4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("target host is too long")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, []byte(host)...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	request = append(request, portBytes...)
	if _, err := conn.Write(request); err != nil {
		return fmt.Errorf("proxy connect failed: %w", err)
	}
	return readSocks5ConnectReply(conn)
}

func readSocks5ConnectReply(conn net.Conn) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("proxy connect reply read failed: %w", err)
	}
	if header[0] != 0x05 {
		return errors.New("proxy connect reply is not socks5")
	}
	if header[1] != 0x00 {
		return fmt.Errorf("proxy connect rejected with code %d", header[1])
	}
	var toRead int
	switch header[3] {
	case 0x01:
		toRead = 4
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return err
		}
		toRead = int(length[0])
	case 0x04:
		toRead = 16
	default:
		return fmt.Errorf("proxy connect reply address type %d is unsupported", header[3])
	}
	if toRead > 0 {
		if _, err := io.CopyN(io.Discard, conn, int64(toRead)); err != nil {
			return err
		}
	}
	if _, err := io.CopyN(io.Discard, conn, 2); err != nil {
		return err
	}
	return nil
}
