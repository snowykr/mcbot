package mcserver

import (
	"time"

	"github.com/snowy/mcbot/internal/state"
)

type PresenceState struct {
	ServerState       state.ServerState
	ContainerRunning  bool
	Players           []string
	LastStartTime     time.Time
	LastReadyDuration time.Duration
}
