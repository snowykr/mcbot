package mcserver

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/dockerctl"
)

type mockLogMultiplexer struct {
	ch chan dockerctl.LogLine
}

func newMockLogMultiplexer() *mockLogMultiplexer {
	return &mockLogMultiplexer{
		ch: make(chan dockerctl.LogLine, 100),
	}
}

func (m *mockLogMultiplexer) Subscribe() <-chan dockerctl.LogLine {
	return m.ch
}

func (m *mockLogMultiplexer) sendLog(text string) {
	m.ch <- dockerctl.LogLine{Text: text}
}

func (m *mockLogMultiplexer) sendError(err error) {
	m.ch <- dockerctl.LogLine{Err: err}
}

func (m *mockLogMultiplexer) close() {
	close(m.ch)
}

func TestPlayerTracker_JoinPattern(t *testing.T) {
	tests := []struct {
		name        string
		logLine     string
		joinPattern string
		shouldMatch bool
		playerName  string
	}{
		{
			name:        "Standard join log",
			logLine:     "[12:34:56] [Server thread/INFO]: Player123 joined the game",
			joinPattern: `joined the game`,
			shouldMatch: false,
		},
		{
			name:        "Join with capture group",
			logLine:     "[12:34:56] [Server thread/INFO]: Player123 joined the game",
			joinPattern: `(\w+) joined the game`,
			shouldMatch: true,
			playerName:  "Player123",
		},
		{
			name:        "Join with different format",
			logLine:     "Player456 has joined",
			joinPattern: `(\w+) has joined`,
			shouldMatch: true,
			playerName:  "Player456",
		},
		{
			name:        "Non-matching log",
			logLine:     "[12:34:56] [Server thread/INFO]: Server started",
			joinPattern: `(\w+) joined the game`,
			shouldMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMux := newMockLogMultiplexer()
			defer mockMux.close()

			tracker, err := NewPlayerTracker(mockMux, tt.joinPattern, `(\w+) left the game`)
			if err != nil {
				t.Fatalf("Failed to create tracker: %v", err)
			}

			mockMux.sendLog(tt.logLine)
			time.Sleep(10 * time.Millisecond)

			players := tracker.GetPlayers()

			if tt.shouldMatch {
				if len(players) != 1 {
					t.Fatalf("Expected 1 player, got %d", len(players))
				}
				if players[0] != tt.playerName {
					t.Errorf("Expected player %s, got %s", tt.playerName, players[0])
				}
			} else {
				if len(players) != 0 {
					t.Errorf("Expected 0 players, got %d", len(players))
				}
			}
		})
	}
}

func TestPlayerTracker_LeavePattern(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined the game`, `(\w+) left the game`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	mockMux.sendLog("Player1 joined the game")
	mockMux.sendLog("Player2 joined the game")
	time.Sleep(10 * time.Millisecond)

	players := tracker.GetPlayers()
	if len(players) != 2 {
		t.Fatalf("Expected 2 players after joins, got %d", len(players))
	}

	mockMux.sendLog("Player1 left the game")
	time.Sleep(10 * time.Millisecond)

	players = tracker.GetPlayers()
	if len(players) != 1 {
		t.Fatalf("Expected 1 player after leave, got %d", len(players))
	}
	if players[0] != "Player2" {
		t.Errorf("Expected Player2 to remain, got %s", players[0])
	}
}

func TestPlayerTracker_ConcurrentJoinsAndLeaves(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	var wg sync.WaitGroup
	numPlayers := 50

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numPlayers; i++ {
			playerName := "Player" + string(rune('A'+(i%26)))
			if i >= 26 {
				playerName = playerName + string(rune('0'+(i/26)))
			}
			mockMux.sendLog(playerName + " joined")
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			tracker.GetPlayers()
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	players := tracker.GetPlayers()
	if len(players) != numPlayers {
		t.Errorf("Expected %d players, got %d", numPlayers, len(players))
	}
}

func TestPlayerTracker_OnChangeCallback(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	var mu sync.Mutex
	callbackCount := 0
	var lastPlayers []string

	tracker.SetOnChange(func(players []string) {
		mu.Lock()
		defer mu.Unlock()
		callbackCount++
		lastPlayers = make([]string, len(players))
		copy(lastPlayers, players)
	})

	mockMux.sendLog("Alice joined")
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	if callbackCount != 1 {
		t.Errorf("Expected 1 callback, got %d", callbackCount)
	}
	if len(lastPlayers) != 1 || lastPlayers[0] != "Alice" {
		t.Errorf("Expected [Alice], got %v", lastPlayers)
	}
	mu.Unlock()

	mockMux.sendLog("Bob joined")
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	if callbackCount != 2 {
		t.Errorf("Expected 2 callbacks, got %d", callbackCount)
	}
	sort.Strings(lastPlayers)
	expected := []string{"Alice", "Bob"}
	sort.Strings(expected)
	if len(lastPlayers) != 2 || lastPlayers[0] != expected[0] || lastPlayers[1] != expected[1] {
		t.Errorf("Expected %v, got %v", expected, lastPlayers)
	}
	mu.Unlock()

	mockMux.sendLog("Alice left")
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	if callbackCount != 3 {
		t.Errorf("Expected 3 callbacks, got %d", callbackCount)
	}
	if len(lastPlayers) != 1 || lastPlayers[0] != "Bob" {
		t.Errorf("Expected [Bob], got %v", lastPlayers)
	}
	mu.Unlock()
}

func TestPlayerTracker_SetOnChangeRace(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			mockMux.sendLog("Player" + string(rune('A'+i%26)) + " joined")
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			tracker.SetOnChange(func(players []string) {
			})
			time.Sleep(5 * time.Millisecond)
		}
	}()

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	t.Log("SetOnChange race test completed without data race")
}

func TestPlayerTracker_Clear(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	mockMux.sendLog("Player1 joined")
	mockMux.sendLog("Player2 joined")
	mockMux.sendLog("Player3 joined")
	time.Sleep(20 * time.Millisecond)

	players := tracker.GetPlayers()
	if len(players) != 3 {
		t.Fatalf("Expected 3 players before clear, got %d", len(players))
	}

	tracker.Clear()

	players = tracker.GetPlayers()
	if len(players) != 0 {
		t.Errorf("Expected 0 players after clear, got %d", len(players))
	}

	mockMux.sendLog("Player4 joined")
	time.Sleep(20 * time.Millisecond)

	players = tracker.GetPlayers()
	if len(players) != 1 {
		t.Errorf("Expected 1 player after clear and new join, got %d", len(players))
	}
	if players[0] != "Player4" {
		t.Errorf("Expected Player4, got %s", players[0])
	}
}

func TestPlayerTracker_ClearDuringActiveTracking(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			mockMux.sendLog("Player" + string(rune('A'+i%26)) + " joined")
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(25 * time.Millisecond)
		tracker.Clear()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			tracker.GetPlayers()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	t.Log("Clear during active tracking completed without panic")
}

func TestPlayerTracker_DuplicateJoin(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	mockMux.sendLog("Player1 joined")
	mockMux.sendLog("Player1 joined")
	mockMux.sendLog("Player1 joined")
	time.Sleep(20 * time.Millisecond)

	players := tracker.GetPlayers()
	if len(players) != 1 {
		t.Errorf("Expected 1 player (duplicate joins should not add), got %d", len(players))
	}
}

func TestPlayerTracker_LeaveNonExistentPlayer(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	mockMux.sendLog("Player1 joined")
	time.Sleep(10 * time.Millisecond)

	mockMux.sendLog("Player2 left")
	time.Sleep(10 * time.Millisecond)

	players := tracker.GetPlayers()
	if len(players) != 1 {
		t.Errorf("Expected 1 player (Player1), got %d", len(players))
	}
	if players[0] != "Player1" {
		t.Errorf("Expected Player1, got %s", players[0])
	}
}

func TestPlayerTracker_ErrorLogsIgnored(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	mockMux.sendLog("Player1 joined")
	mockMux.sendError(dockerctl.LogLine{Err: err}.Err)
	mockMux.sendLog("Player2 joined")
	time.Sleep(20 * time.Millisecond)

	players := tracker.GetPlayers()
	if len(players) != 2 {
		t.Errorf("Expected 2 players (errors should be ignored), got %d", len(players))
	}
}

func TestPlayerTracker_InvalidRegexPattern(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	_, err := NewPlayerTracker(mockMux, `[invalid(`, `(\w+) left`)
	if err == nil {
		t.Error("Expected error for invalid join pattern, got nil")
	}

	_, err = NewPlayerTracker(mockMux, `(\w+) joined`, `[invalid(`)
	if err == nil {
		t.Error("Expected error for invalid leave pattern, got nil")
	}
}

func TestPlayerTracker_CallbackChangeDuringExecution(t *testing.T) {
	mockMux := newMockLogMultiplexer()
	defer mockMux.close()

	tracker, err := NewPlayerTracker(mockMux, `(\w+) joined`, `(\w+) left`)
	if err != nil {
		t.Fatalf("Failed to create tracker: %v", err)
	}

	var mu sync.Mutex
	callback1Count := 0
	callback2Count := 0

	tracker.SetOnChange(func(players []string) {
		mu.Lock()
		callback1Count++
		mu.Unlock()
	})

	mockMux.sendLog("Player1 joined")
	time.Sleep(20 * time.Millisecond)

	tracker.SetOnChange(func(players []string) {
		mu.Lock()
		callback2Count++
		mu.Unlock()
	})

	mockMux.sendLog("Player2 joined")
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if callback1Count != 1 {
		t.Errorf("Expected callback1 to be called 1 time, got %d", callback1Count)
	}
	if callback2Count != 1 {
		t.Errorf("Expected callback2 to be called 1 time, got %d", callback2Count)
	}
}
