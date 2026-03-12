package rcon

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

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
	port := unusedLocalPort(t)
	client := NewClient("127.0.0.1", port, "secret", 200*time.Millisecond)

	_, err := client.Execute(context.Background(), "list")
	if !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("expected ErrConnectionFailed, got %v", err)
	}
}

func unusedLocalPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate local port: %v", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", listener.Addr())
	}

	return addr.Port
}
