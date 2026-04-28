package rcon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	gorcon "github.com/gorcon/rcon"
	rcontest "github.com/gorcon/rcon/rcontest"
)

func TestNewClient_AddressFormatting(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     int
		expected string
	}{
		{name: "hostname", host: "mc-server", port: 25575, expected: "mc-server:25575"},
		{name: "ipv4", host: "127.0.0.1", port: 25575, expected: "127.0.0.1:25575"},
		{name: "ipv6", host: "2001:db8::1", port: 25575, expected: "[2001:db8::1]:25575"},
		{name: "bracketed ipv6", host: "[2001:db8::1]", port: 25575, expected: "[2001:db8::1]:25575"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.host, tt.port, "secret", time.Second)
			if client.address != tt.expected {
				t.Fatalf("expected address %q, got %q", tt.expected, client.address)
			}
		})
	}
}

func TestClientExecute_EmptyCommand(t *testing.T) {
	client := NewClient("127.0.0.1", 25575, "secret", time.Second)

	_, err := client.Execute(context.Background(), "")
	if !errors.Is(err, ErrEmptyCommand) {
		t.Fatalf("expected ErrEmptyCommand, got %v", err)
	}
}

func TestClientExecute_ContextCanceled(t *testing.T) {
	client := NewClient("127.0.0.1", 1, "secret", time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Execute(ctx, "list")
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("expected ErrCanceled, got %v", err)
	}
}

func TestClientExecute_ContextDeadlineExceeded(t *testing.T) {
	client := NewClient("127.0.0.1", 25575, "secret", time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := client.Execute(ctx, "list")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestClientExecute_ConnectionFailure(t *testing.T) {
	port := abruptCloseServerPort(t)
	client := NewClient("127.0.0.1", port, "secret", 200*time.Millisecond)

	_, err := client.Execute(context.Background(), "list")
	if !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("expected ErrConnectionFailed, got %v", err)
	}
}

func TestClientExecute_AuthFailure(t *testing.T) {
	server := rcontest.NewServer(rcontest.SetSettings(rcontest.Settings{Password: "secret"}))
	defer server.Close()

	host, port := splitServerAddr(t, server.Addr())
	client := NewClient(host, port, "wrong", time.Second)

	_, err := client.Execute(context.Background(), "list")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("expected ErrAuthFailed, got %v", err)
	}
}

func TestClientExecute_CommandFailureWrapsError(t *testing.T) {
	server := rcontest.NewServer(
		rcontest.SetSettings(rcontest.Settings{Password: "secret"}),
		rcontest.SetCommandHandler(func(c *rcontest.Context) {
			_, _ = gorcon.NewPacket(gorcon.SERVERDATA_RESPONSE_VALUE, 42, "bad packet id").WriteTo(c.Conn())
		}),
	)
	defer server.Close()

	host, port := splitServerAddr(t, server.Addr())
	client := NewClient(host, port, "secret", time.Second)

	_, err := client.Execute(context.Background(), "list")
	if err == nil {
		t.Fatal("expected command execution error")
	}
	if !strings.Contains(err.Error(), "명령 실행 실패:") {
		t.Fatalf("expected wrapped command failure, got %v", err)
	}
}

func TestMapTimeoutError_WrappedNetTimeout(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", timeoutNetError{})
	if !errors.Is(mapTimeoutError(err), ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", mapTimeoutError(err))
	}
}

func TestClientExecute_CommandTimeout(t *testing.T) {
	server := rcontest.NewServer(rcontest.SetSettings(rcontest.Settings{
		Password:             "secret",
		CommandResponseDelay: 500 * time.Millisecond,
	}))
	defer server.Close()

	host, port := splitServerAddr(t, server.Addr())
	client := NewClient(host, port, "secret", 100*time.Millisecond)

	_, err := client.Execute(context.Background(), "list")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestClientExecute_ContextCanceledWhileAuthBlocked(t *testing.T) {
	server := rcontest.NewServer(rcontest.SetSettings(rcontest.Settings{
		Password:          "secret",
		AuthResponseDelay: 300 * time.Millisecond,
	}))
	defer server.Close()

	host, port := splitServerAddr(t, server.Addr())
	client := NewClient(host, port, "secret", 2*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)

	start := time.Now()
	go func() {
		_, err := client.Execute(ctx, "list")
		resultCh <- err
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("expected ErrCanceled, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Fatalf("Execute returned after %s, want prompt cancellation", elapsed)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Execute did not return promptly after context cancellation")
	}
}

func TestClientExecute_ContextCanceledWhileCommandBlocked(t *testing.T) {
	server := rcontest.NewServer(rcontest.SetSettings(rcontest.Settings{
		Password:             "secret",
		CommandResponseDelay: 300 * time.Millisecond,
	}))
	defer server.Close()

	host, port := splitServerAddr(t, server.Addr())
	client := NewClient(host, port, "secret", 2*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)

	start := time.Now()
	go func() {
		_, err := client.Execute(ctx, "list")
		resultCh <- err
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("expected ErrCanceled, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Fatalf("Execute returned after %s, want prompt cancellation", elapsed)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Execute did not return promptly after context cancellation")
	}
}

func splitServerAddr(t *testing.T, addr string) (string, int) {
	t.Helper()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("failed to split server address %q: %v", addr, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("failed to parse server port %q: %v", portStr, err)
	}

	return host, port
}

func abruptCloseServerPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create test listener: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", listener.Addr())
	}

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
		_ = listener.Close()
	}()

	return addr.Port
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return false }
