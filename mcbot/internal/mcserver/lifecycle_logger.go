package mcserver

import (
	"time"

	"github.com/snowy/mcbot/internal/state"
)

type LifecycleEvent string

const (
	EventServerStartRequested LifecycleEvent = "server_start_requested"
	EventServerStarted        LifecycleEvent = "server_started"
	EventServerStartFailed    LifecycleEvent = "server_start_failed"
	EventServerStopRequested  LifecycleEvent = "server_stop_requested"
	EventServerStopped        LifecycleEvent = "server_stopped"
	EventServerStopFailed     LifecycleEvent = "server_stop_failed"
	EventServerCrashed        LifecycleEvent = "server_crashed"
	EventAutoRestartScheduled LifecycleEvent = "auto_restart_scheduled"
	EventAutoRestartSucceeded LifecycleEvent = "auto_restart_succeeded"
	EventAutoRestartFailed    LifecycleEvent = "auto_restart_failed"
)

type LifecycleLogger interface {
	OnServerStartRequested()
	OnServerStarted(readyDuration time.Duration, loadSeconds float64)
	OnServerStartFailed(reason string, err error)
	OnServerStopRequested()
	OnServerStopped()
	OnServerStopFailed(reason string, err error)
	OnServerCrashed(reason string, info state.Info, containerExists bool)
}

type NoopLifecycleLogger struct{}

func (n *NoopLifecycleLogger) OnServerStartRequested() {}
func (n *NoopLifecycleLogger) OnServerStarted(readyDuration time.Duration, loadSeconds float64) {
}
func (n *NoopLifecycleLogger) OnServerStartFailed(reason string, err error) {}
func (n *NoopLifecycleLogger) OnServerStopRequested()                       {}
func (n *NoopLifecycleLogger) OnServerStopped()                             {}
func (n *NoopLifecycleLogger) OnServerStopFailed(reason string, err error)  {}
func (n *NoopLifecycleLogger) OnServerCrashed(reason string, info state.Info, containerExists bool) {
}
