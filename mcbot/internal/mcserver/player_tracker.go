package mcserver

import (
	"log"
	"regexp"
	"sync"

	"github.com/snowy/mcbot/internal/dockerctl"
)

type LogSubscriber interface {
	Subscribe() <-chan dockerctl.LogLine
}

type PlayerTracker struct {
	players      map[string]struct{}
	mu           sync.RWMutex
	joinPattern  *regexp.Regexp
	leavePattern *regexp.Regexp
	onChange     func([]string)
}

func NewPlayerTracker(subscriber LogSubscriber, joinPattern, leavePattern string) (*PlayerTracker, error) {
	joinRegex, err := regexp.Compile(joinPattern)
	if err != nil {
		return nil, err
	}

	leaveRegex, err := regexp.Compile(leavePattern)
	if err != nil {
		return nil, err
	}

	tracker := &PlayerTracker{
		players:      make(map[string]struct{}),
		joinPattern:  joinRegex,
		leavePattern: leaveRegex,
	}

	logCh := subscriber.Subscribe()
	go tracker.processLogs(logCh)

	return tracker, nil
}

func (t *PlayerTracker) processLogs(logCh <-chan dockerctl.LogLine) {
	for logLine := range logCh {
		if logLine.Err != nil {
			continue
		}

		if matches := t.joinPattern.FindStringSubmatch(logLine.Text); len(matches) >= 2 {
			playerName := matches[1]
			t.mu.Lock()
			t.players[playerName] = struct{}{}
			players := t.getPlayersLocked()
			callback := t.onChange
			t.mu.Unlock()

			log.Printf("플레이어 접속: %s (현재 %d명)", playerName, len(players))

			if callback != nil {
				callback(players)
			}
			continue
		}

		if matches := t.leavePattern.FindStringSubmatch(logLine.Text); len(matches) >= 2 {
			playerName := matches[1]
			t.mu.Lock()
			delete(t.players, playerName)
			players := t.getPlayersLocked()
			callback := t.onChange
			t.mu.Unlock()

			log.Printf("플레이어 퇴장: %s (현재 %d명)", playerName, len(players))

			if callback != nil {
				callback(players)
			}
		}
	}
}

func (t *PlayerTracker) GetPlayers() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.getPlayersLocked()
}

func (t *PlayerTracker) getPlayersLocked() []string {
	players := make([]string, 0, len(t.players))
	for name := range t.players {
		players = append(players, name)
	}
	return players
}

func (t *PlayerTracker) SetOnChange(callback func([]string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onChange = callback
}

func (t *PlayerTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.players = make(map[string]struct{})
}
