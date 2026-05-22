package proxyclient

import (
	"bytes"
	"io"
	"net"
	"net/url"
	"testing"
)

func TestSocks5HandshakeUsesRemoteDNSForHostnames(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan []byte, 1)
	go func() {
		defer server.Close()
		greeting := make([]byte, 3)
		_, _ = io.ReadFull(server, greeting)
		_, _ = server.Write([]byte{0x05, 0x00})
		request := make([]byte, 4+1+len("api.openai.com")+2)
		_, _ = io.ReadFull(server, request)
		done <- request
		_, _ = server.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
	}()
	parsed, err := url.Parse("socks5h://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if err := socks5Handshake(client, parsed, "api.openai.com:443"); err != nil {
		t.Fatalf("socks5Handshake() error = %v", err)
	}
	request := <-done
	if len(request) < 7 {
		t.Fatalf("request too short: %v", request)
	}
	if request[3] != 0x03 {
		t.Fatalf("address type = 0x%x, want domain-name 0x03", request[3])
	}
	length := int(request[4])
	if length != len("api.openai.com") {
		t.Fatalf("domain length = %d", length)
	}
	if got := string(request[5 : 5+length]); got != "api.openai.com" {
		t.Fatalf("domain = %q", got)
	}
	if bytes.Contains(request, []byte{104, 18, 12, 123}) {
		t.Fatalf("request unexpectedly contained a resolved IPv4 address: %v", request)
	}
}
