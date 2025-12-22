package mcserver

import (
	"sync"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/dockerctl"
)

func TestLogMultiplexer_BasicSubscription(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Stop()

	sub := mux.Subscribe()

	select {
	case <-sub:
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLogMultiplexer_MultipleSubscribers(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Stop()

	sub1 := mux.Subscribe()
	sub2 := mux.Subscribe()
	sub3 := mux.Subscribe()

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for range sub1 {
		}
	}()

	go func() {
		defer wg.Done()
		for range sub2 {
		}
	}()

	go func() {
		defer wg.Done()
		for range sub3 {
		}
	}()

	time.Sleep(50 * time.Millisecond)
	mux.Stop()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("All subscribers closed successfully")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for subscribers to close")
	}
}

func TestLogMultiplexer_StopIdempotent(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())

	mux.Stop()
	mux.Stop()
	mux.Stop()

	t.Log("Multiple Stop calls completed without panic")
}

func TestLogMultiplexer_SubscribeAfterStop(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	mux.Stop()

	sub := mux.Subscribe()

	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("Expected closed channel, but received a value")
		}
		t.Log("Received closed channel as expected")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for channel to be closed")
	}
}

func TestLogMultiplexer_StopWithoutStart(t *testing.T) {
	mux := NewLogMultiplexer("test_container")

	mux.Stop()

	t.Log("Stop without Start completed successfully")
}

func TestLogMultiplexer_ConcurrentSubscribe(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Stop()

	var wg sync.WaitGroup
	subscribers := make([]<-chan dockerctl.LogLine, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			subscribers[idx] = mux.Subscribe()
		}(i)
	}

	wg.Wait()

	for i, sub := range subscribers {
		if sub == nil {
			t.Fatalf("Subscriber %d is nil", i)
		}
	}

	t.Log("Concurrent subscription completed successfully")
}

func TestLogMultiplexer_ConcurrentStopAndSubscribe(t *testing.T) {
	for i := 0; i < 20; i++ {
		mux := NewLogMultiplexer("test_container")
		mux.Start(time.Now())

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i) * time.Millisecond)
			mux.Stop()
		}()

		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				sub := mux.Subscribe()
				go func(ch <-chan dockerctl.LogLine) {
					for range ch {
					}
				}(sub)
				time.Sleep(time.Millisecond)
			}
		}()

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout in concurrent stop and subscribe test")
		}
	}

	t.Log("Concurrent stop and subscribe test completed")
}

func TestLogMultiplexer_StartOnce(t *testing.T) {
	mux := NewLogMultiplexer("test_container")

	mux.Start(time.Now())
	mux.Start(time.Now())
	mux.Start(time.Now())

	defer mux.Stop()

	t.Log("Multiple Start calls completed (only first should execute)")
}

func TestLogMultiplexer_SubscriberChannelCloseOnStop(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())

	sub1 := mux.Subscribe()
	sub2 := mux.Subscribe()

	mux.Stop()

	for i, sub := range []<-chan dockerctl.LogLine{sub1, sub2} {
		select {
		case _, ok := <-sub:
			if ok {
				t.Fatalf("Subscriber %d: expected closed channel", i)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Subscriber %d: timeout waiting for channel close", i)
		}
	}

	t.Log("All subscriber channels closed on Stop")
}

func TestLogMultiplexer_NoDeadlockOnSlowSubscriber(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Stop()

	slowSub := mux.Subscribe()
	fastSub := mux.Subscribe()

	go func() {
		for range slowSub {
			time.Sleep(100 * time.Millisecond)
		}
	}()

	go func() {
		for range fastSub {
		}
	}()

	time.Sleep(200 * time.Millisecond)

	t.Log("No deadlock with slow subscriber")
}

func TestLogMultiplexer_RapidStartStop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Panic occurred: %v", r)
		}
	}()

	for i := 0; i < 50; i++ {
		mux := NewLogMultiplexer("test_container")
		mux.Start(time.Now())

		sub := mux.Subscribe()
		go func() {
			for range sub {
			}
		}()

		mux.Stop()
	}

	t.Log("Rapid start/stop completed without panic")
}
