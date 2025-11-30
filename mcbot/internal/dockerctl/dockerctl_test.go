package dockerctl

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestFollowLogs_NormalTermination(t *testing.T) {
	ctx := context.Background()

	logCh := FollowLogs(ctx, "echo_test_container_that_does_not_exist", time.Now())

	var lines []LogLine
	for line := range logCh {
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		t.Log("No lines received (expected for non-existent container)")
	}

	for _, line := range lines {
		if line.Err != nil {
			t.Logf("Received error (expected): %v", line.Err)
		}
	}
}

func TestFollowLogs_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	logCh := FollowLogs(ctx, "nonexistent_container_for_test", time.Now())

	time.Sleep(50 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		for range logCh {
		}
		close(done)
	}()

	select {
	case <-done:
		t.Log("Channel closed successfully after context cancellation")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for channel to close after context cancellation")
	}
}

func TestFollowLogs_ImmediateContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	logCh := FollowLogs(ctx, "nonexistent_container", time.Now())

	done := make(chan struct{})
	go func() {
		for range logCh {
		}
		close(done)
	}()

	select {
	case <-done:
		t.Log("Channel closed successfully with pre-cancelled context")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for channel to close with pre-cancelled context")
	}
}

func TestFollowLogs_TimeoutContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	logCh := FollowLogs(ctx, "nonexistent_container_timeout_test", time.Now())

	done := make(chan struct{})
	go func() {
		for range logCh {
		}
		close(done)
	}()

	select {
	case <-done:
		t.Log("Channel closed successfully after timeout")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for channel to close after context timeout")
	}
}

func TestFollowLogs_ConcurrentCancellation(t *testing.T) {
	for i := 0; i < 10; i++ {
		t.Run("iteration", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())

			logCh := FollowLogs(ctx, "nonexistent_container_concurrent", time.Now())

			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()
				time.Sleep(time.Duration(i*10) * time.Millisecond)
				cancel()
			}()

			go func() {
				defer wg.Done()
				for range logCh {
				}
			}()

			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Timeout in concurrent cancellation test")
			}
		})
	}
}

func TestFollowLogs_NoPanicOnRapidCancellation(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Panic occurred: %v", r)
		}
	}()

	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithCancel(context.Background())

		logCh := FollowLogs(ctx, "test_container_rapid", time.Now())

		cancel()

		for range logCh {
		}
	}

	t.Log("No panic occurred during rapid cancellation tests")
}
