package botapp

import (
	"log"
	"time"

	"github.com/snowy/mcbot/internal/state"
)

type StdLifecycleLogger struct{}

func NewStdLifecycleLogger() *StdLifecycleLogger {
	return &StdLifecycleLogger{}
}

func (l *StdLifecycleLogger) OnServerStartRequested() {
	log.Printf("[LIFECYCLE] event=server_start_requested")
}

func (l *StdLifecycleLogger) OnServerStarted(readyDuration time.Duration, loadSeconds float64) {
	log.Printf("[LIFECYCLE] event=server_started ready_duration=%.2fs load_seconds=%.2f",
		readyDuration.Seconds(), loadSeconds)
}

func (l *StdLifecycleLogger) OnServerStartFailed(reason string, err error) {
	if err != nil {
		log.Printf("[LIFECYCLE] event=server_start_failed reason=%s error=\"%v\"", reason, err)
	} else {
		log.Printf("[LIFECYCLE] event=server_start_failed reason=%s", reason)
	}
}

func (l *StdLifecycleLogger) OnServerStopRequested() {
	log.Printf("[LIFECYCLE] event=server_stop_requested")
}

func (l *StdLifecycleLogger) OnServerStopped() {
	log.Printf("[LIFECYCLE] event=server_stopped")
}

func (l *StdLifecycleLogger) OnServerStopFailed(reason string, err error) {
	if err != nil {
		log.Printf("[LIFECYCLE] event=server_stop_failed reason=%s error=\"%v\"", reason, err)
	} else {
		log.Printf("[LIFECYCLE] event=server_stop_failed reason=%s", reason)
	}
}

func (l *StdLifecycleLogger) OnServerCrashed(reason string, info state.Info, containerExists bool) {
	log.Printf("[LIFECYCLE] event=server_crashed reason=%s last_state=%s container_exists=%t",
		reason, info.State.String(), containerExists)
	if info.LastError != nil {
		log.Printf("[LIFECYCLE] event=server_crashed last_error=\"%v\"", info.LastError)
	}
}
