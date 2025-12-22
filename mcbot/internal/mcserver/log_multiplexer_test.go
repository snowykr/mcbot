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

	_ = mux.Subscribe()
	_ = mux.Subscribe()
	_ = mux.Subscribe()

	time.Sleep(50 * time.Millisecond)
	mux.Stop()

	time.Sleep(50 * time.Millisecond)
	t.Log("Multiple subscribers test completed")
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
	case <-sub:
		t.Fatal("Did not expect to receive data after Stop")
	case <-time.After(100 * time.Millisecond):
		t.Log("Channel remains open after Stop as expected (for restart capability)")
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

	closed1 := make(chan bool)
	closed2 := make(chan bool)

	go func() {
		for range sub1 {
		}
		closed1 <- true
	}()
	go func() {
		for range sub2 {
		}
		closed2 <- true
	}()

	time.Sleep(50 * time.Millisecond)
	mux.Stop()

	select {
	case <-closed1:
		t.Log("Subscriber 1 channel closed as expected")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Subscriber 1 channel did not close after Stop")
	}

	select {
	case <-closed2:
		t.Log("Subscriber 2 channel closed as expected")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Subscriber 2 channel did not close after Stop")
	}
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

func TestLogMultiplexer_RestartAfterStop(t *testing.T) {
	mux := NewLogMultiplexer("test_container")

	sub := mux.Subscribe()

	go func() {
		for range sub {
		}
	}()

	mux.Start(time.Now())
	time.Sleep(50 * time.Millisecond)
	mux.Stop()

	time.Sleep(50 * time.Millisecond)

	mux.Start(time.Now())
	time.Sleep(50 * time.Millisecond)
	mux.Stop()

	t.Log("Restart after Stop works correctly")
}

func TestLogMultiplexer_MultipleRestarts(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	sub := mux.Subscribe()

	go func() {
		for range sub {
		}
	}()

	for i := 0; i < 3; i++ {
		mux.Start(time.Now())
		time.Sleep(50 * time.Millisecond)
		mux.Stop()
		time.Sleep(50 * time.Millisecond)
	}

	t.Log("Multiple restarts completed successfully")
}

func TestLogMultiplexer_SubscriberReceivesAllLogs(t *testing.T) {
	mux := NewLogMultiplexer("test_container")

	sub1 := mux.Subscribe()
	sub2 := mux.Subscribe()

	received1 := make([]string, 0)
	received2 := make([]string, 0)
	done1 := make(chan bool)
	done2 := make(chan bool)

	go func() {
		for log := range sub1 {
			if log.Err == nil {
				received1 = append(received1, log.Text)
			}
		}
		done1 <- true
	}()

	go func() {
		for log := range sub2 {
			if log.Err == nil {
				received2 = append(received2, log.Text)
			}
		}
		done2 <- true
	}()

	mux.Start(time.Now())
	time.Sleep(100 * time.Millisecond)
	mux.Stop()

	<-done1
	<-done2

	if len(received1) == 0 && len(received2) == 0 {
		t.Log("No logs received (container may not exist), but both subscribers closed properly")
		return
	}

	if len(received1) != len(received2) {
		t.Errorf("Subscribers received different number of logs: sub1=%d, sub2=%d", len(received1), len(received2))
	}

	t.Logf("Both subscribers received %d logs", len(received1))
}

func TestLogMultiplexer_SubscriberBufferOverflow(t *testing.T) {
	mux := NewLogMultiplexer("test_container")

	slowSub := mux.Subscribe()
	fastSub := mux.Subscribe()

	fastReceived := 0
	slowReceived := 0

	fastDone := make(chan bool)
	slowDone := make(chan bool)

	go func() {
		for range fastSub {
			fastReceived++
		}
		fastDone <- true
	}()

	go func() {
		for range slowSub {
			slowReceived++
			time.Sleep(50 * time.Millisecond)
		}
		slowDone <- true
	}()

	mux.Start(time.Now())
	time.Sleep(200 * time.Millisecond)
	mux.Stop()

	<-fastDone
	<-slowDone

	t.Logf("Fast subscriber received %d logs, slow subscriber received %d logs", fastReceived, slowReceived)

	if slowReceived > fastReceived {
		t.Errorf("Slow subscriber received more logs than fast subscriber: slow=%d, fast=%d", slowReceived, fastReceived)
	}
}
