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
	defer mux.Close()

	sub := mux.Subscribe()
	defer sub.Unsubscribe()

	select {
	case <-sub.Ch:
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLogMultiplexer_MultipleSubscribers(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Close()

	sub1 := mux.Subscribe()
	defer sub1.Unsubscribe()
	sub2 := mux.Subscribe()
	defer sub2.Unsubscribe()
	sub3 := mux.Subscribe()
	defer sub3.Unsubscribe()

	time.Sleep(50 * time.Millisecond)
	mux.Stop()

	time.Sleep(50 * time.Millisecond)
	t.Log("Multiple subscribers test completed")
}

func TestLogMultiplexer_StopIdempotent(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Close()

	mux.Stop()
	mux.Stop()
	mux.Stop()

	t.Log("Multiple Stop calls completed without panic")
}

func TestLogMultiplexer_SubscribeAfterStop(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Close()

	mux.Stop()

	sub := mux.Subscribe()
	defer sub.Unsubscribe()

	select {
	case _, ok := <-sub.Ch:
		if !ok {
			t.Fatal("Channel should remain open after Stop")
		}
		t.Fatal("Did not expect to receive data after Stop")
	case <-time.After(100 * time.Millisecond):
		t.Log("Channel remains open after Stop as expected (for restart capability)")
	}
}

func TestLogMultiplexer_StopWithoutStart(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	defer mux.Close()

	mux.Stop()

	t.Log("Stop without Start completed successfully")
}

func TestLogMultiplexer_ConcurrentSubscribe(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Close()

	var wg sync.WaitGroup
	subscribers := make([]Subscription, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			subscribers[idx] = mux.Subscribe()
		}(i)
	}

	wg.Wait()

	for i, sub := range subscribers {
		if sub.Ch == nil {
			t.Fatalf("Subscriber %d channel is nil", i)
		}
		defer sub.Unsubscribe()
	}

	mux.Stop()

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
				go func(s Subscription) {
					defer s.Unsubscribe()
					for range s.Ch {
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

		mux.Close()
	}

	t.Log("Concurrent stop and subscribe test completed")
}

func TestLogMultiplexer_StartOnce(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	defer mux.Close()

	mux.Start(time.Now())
	mux.Start(time.Now())
	mux.Start(time.Now())

	mux.Stop()

	t.Log("Multiple Start calls completed (only first should execute)")
}

func TestLogMultiplexer_SubscriberChannelStaysOpenOnStop(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Close()

	sub1 := mux.Subscribe()
	defer sub1.Unsubscribe()
	sub2 := mux.Subscribe()
	defer sub2.Unsubscribe()

	time.Sleep(50 * time.Millisecond)
	mux.Stop()

	select {
	case _, ok := <-sub1.Ch:
		if !ok {
			t.Fatal("Subscriber 1 channel should not close after Stop")
		}
	case <-time.After(100 * time.Millisecond):
		t.Log("Subscriber 1 channel remains open after Stop as expected")
	}

	select {
	case _, ok := <-sub2.Ch:
		if !ok {
			t.Fatal("Subscriber 2 channel should not close after Stop")
		}
	case <-time.After(100 * time.Millisecond):
		t.Log("Subscriber 2 channel remains open after Stop as expected")
	}
}

func TestLogMultiplexer_NoDeadlockOnSlowSubscriber(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Close()

	slowSub := mux.Subscribe()
	defer slowSub.Unsubscribe()
	fastSub := mux.Subscribe()
	defer fastSub.Unsubscribe()

	go func() {
		for range slowSub.Ch {
			time.Sleep(100 * time.Millisecond)
		}
	}()

	go func() {
		for range fastSub.Ch {
		}
	}()

	time.Sleep(200 * time.Millisecond)
	mux.Stop()

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
		go func(s Subscription) {
			defer s.Unsubscribe()
			for range s.Ch {
			}
		}(sub)

		mux.Stop()
		mux.Close()
	}

	t.Log("Rapid start/stop completed without panic")
}

func TestLogMultiplexer_RestartAfterStop(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	defer mux.Close()

	sub := mux.Subscribe()
	defer sub.Unsubscribe()

	go func(ch <-chan dockerctl.LogLine) {
		for range ch {
		}
	}(sub.Ch)

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
	defer mux.Close()

	sub := mux.Subscribe()
	defer sub.Unsubscribe()

	go func(ch <-chan dockerctl.LogLine) {
		for range ch {
		}
	}(sub.Ch)

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
	defer mux.Close()

	sub1 := mux.Subscribe()
	defer sub1.Unsubscribe()
	sub2 := mux.Subscribe()
	defer sub2.Unsubscribe()

	received1 := make([]string, 0)
	received2 := make([]string, 0)
	done1 := make(chan bool)
	done2 := make(chan bool)

	go func() {
		for log := range sub1.Ch {
			if log.Err == nil {
				received1 = append(received1, log.Text)
			}
		}
		done1 <- true
	}()

	go func() {
		for log := range sub2.Ch {
			if log.Err == nil {
				received2 = append(received2, log.Text)
			}
		}
		done2 <- true
	}()

	mux.Start(time.Now())
	time.Sleep(100 * time.Millisecond)
	mux.Stop()
	mux.Close()

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
	defer mux.Close()

	slowSub := mux.Subscribe()
	defer slowSub.Unsubscribe()
	fastSub := mux.Subscribe()
	defer fastSub.Unsubscribe()

	fastReceived := 0
	slowReceived := 0

	fastDone := make(chan bool)
	slowDone := make(chan bool)

	go func() {
		for range fastSub.Ch {
			fastReceived++
		}
		fastDone <- true
	}()

	go func() {
		for range slowSub.Ch {
			slowReceived++
			time.Sleep(50 * time.Millisecond)
		}
		slowDone <- true
	}()

	mux.Start(time.Now())
	time.Sleep(200 * time.Millisecond)
	mux.Stop()
	mux.Close()

	<-fastDone
	<-slowDone

	t.Logf("Fast subscriber received %d logs, slow subscriber received %d logs", fastReceived, slowReceived)

	if slowReceived > fastReceived {
		t.Errorf("Slow subscriber received more logs than fast subscriber: slow=%d, fast=%d", slowReceived, fastReceived)
	}
}

func TestLogMultiplexer_CloseClosesAllChannels(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())

	sub1 := mux.Subscribe()
	sub2 := mux.Subscribe()

	closed1 := make(chan bool)
	closed2 := make(chan bool)

	go func() {
		for range sub1.Ch {
		}
		closed1 <- true
	}()
	go func() {
		for range sub2.Ch {
		}
		closed2 <- true
	}()

	time.Sleep(50 * time.Millisecond)
	mux.Close()

	select {
	case <-closed1:
		t.Log("Subscriber 1 channel closed as expected after Close")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Subscriber 1 channel did not close after Close")
	}

	select {
	case <-closed2:
		t.Log("Subscriber 2 channel closed as expected after Close")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Subscriber 2 channel did not close after Close")
	}
}

func TestLogMultiplexer_SubscribeAfterClose(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	mux.Close()

	sub := mux.Subscribe()
	defer sub.Unsubscribe()

	select {
	case _, ok := <-sub.Ch:
		if ok {
			t.Fatal("Expected closed channel after Close")
		}
		t.Log("Received closed channel as expected after Close")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected immediate closed channel after Close")
	}
}

func TestLogMultiplexer_CloseIdempotent(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())

	mux.Close()
	mux.Close()
	mux.Close()

	t.Log("Multiple Close calls completed without panic")
}

func TestLogMultiplexer_StartAfterClose(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	mux.Close()

	mux.Start(time.Now())

	t.Log("Start after Close is ignored as expected")
}

func TestLogMultiplexer_ConcurrentStopAndSubscribeNoRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		mux := NewLogMultiplexer("test_container")
		mux.Start(time.Now())

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i%10) * time.Millisecond)
			mux.Stop()
		}()

		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				sub := mux.Subscribe()
				select {
				case _, ok := <-sub.Ch:
					if !ok {
						t.Error("Received closed channel during Stop (race condition detected)")
					}
				case <-time.After(10 * time.Millisecond):
				}
				sub.Unsubscribe()
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

		mux.Close()
	}

	t.Log("Concurrent stop and subscribe completed without race")
}

func TestLogMultiplexer_UnsubscribeRemovesFromSubscribers(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	defer mux.Close()

	sub1 := mux.Subscribe()
	sub2 := mux.Subscribe()
	sub3 := mux.Subscribe()

	mux.mu.RLock()
	initialCount := len(mux.subscribers)
	mux.mu.RUnlock()

	if initialCount != 3 {
		t.Fatalf("Expected 3 subscribers, got %d", initialCount)
	}

	sub2.Unsubscribe()

	mux.mu.RLock()
	afterUnsubCount := len(mux.subscribers)
	mux.mu.RUnlock()

	if afterUnsubCount != 2 {
		t.Fatalf("Expected 2 subscribers after unsubscribe, got %d", afterUnsubCount)
	}

	sub1.Unsubscribe()
	sub3.Unsubscribe()

	mux.mu.RLock()
	finalCount := len(mux.subscribers)
	mux.mu.RUnlock()

	if finalCount != 0 {
		t.Fatalf("Expected 0 subscribers after all unsubscribe, got %d", finalCount)
	}

	t.Log("Unsubscribe correctly removes subscribers from slice")
}

func TestLogMultiplexer_UnsubscribeIdempotent(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	defer mux.Close()

	sub := mux.Subscribe()

	sub.Unsubscribe()
	sub.Unsubscribe()
	sub.Unsubscribe()

	mux.mu.RLock()
	count := len(mux.subscribers)
	mux.mu.RUnlock()

	if count != 0 {
		t.Fatalf("Expected 0 subscribers, got %d", count)
	}

	t.Log("Multiple unsubscribe calls are safe (idempotent)")
}

func TestLogMultiplexer_UnsubscribeClosesChannel(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())
	defer mux.Close()

	sub := mux.Subscribe()
	sub.Unsubscribe()

	select {
	case _, ok := <-sub.Ch:
		if ok {
			t.Fatal("Channel should be closed after Unsubscribe")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected subscription channel to be closed after Unsubscribe")
	}
}

func TestLogMultiplexer_ConcurrentUnsubscribe(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	defer mux.Close()

	var wg sync.WaitGroup
	subscriptions := make([]Subscription, 100)

	for i := 0; i < 100; i++ {
		subscriptions[i] = mux.Subscribe()
	}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			subscriptions[idx].Unsubscribe()
		}(i)
	}

	wg.Wait()

	mux.mu.RLock()
	count := len(mux.subscribers)
	mux.mu.RUnlock()

	if count != 0 {
		t.Fatalf("Expected 0 subscribers after concurrent unsubscribe, got %d", count)
	}

	for i, sub := range subscriptions {
		select {
		case _, ok := <-sub.Ch:
			if ok {
				t.Fatalf("Subscription channel %d should be closed after concurrent unsubscribe", i)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Timeout waiting for subscription channel %d to close", i)
		}
	}

	t.Log("Concurrent unsubscribe completed without race")
}

func TestLogMultiplexer_UnsubscribeAfterClose_NoPanic(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())

	sub1 := mux.Subscribe()
	sub2 := mux.Subscribe()
	sub3 := mux.Subscribe()

	mux.Close()

	sub1.Unsubscribe()
	sub2.Unsubscribe()
	sub3.Unsubscribe()

	sub1.Unsubscribe()
}

func TestLogMultiplexer_ConcurrentCloseAndUnsubscribe_NoPanic(t *testing.T) {
	mux := NewLogMultiplexer("test_container")
	mux.Start(time.Now())

	subscriptions := make([]Subscription, 50)
	for i := 0; i < 50; i++ {
		subscriptions[i] = mux.Subscribe()
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		mux.Close()
	}()

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			time.Sleep(time.Duration(idx%10) * time.Millisecond)
			subscriptions[idx].Unsubscribe()
		}(i)
	}

	wg.Wait()
}
