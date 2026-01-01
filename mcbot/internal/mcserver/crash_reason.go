package mcserver

const (
	ReasonRuntimeContainerStopped        = "runtime_container_stopped"
	ReasonRuntimeInspectFailedRepeatedly = "runtime_inspect_failed_repeatedly"
	ReasonRuntimeFailurePrefix           = "runtime_failure_"
	ReasonStatusContainerNotRunning      = "status_container_not_running"
	ReasonSyncDetectedUnexpectedStop     = "sync_detected_unexpected_stop"
	ReasonSyncLogStreamEnded             = "sync_log_stream_ended"
	ReasonSyncContainerInspectFailed     = "sync_container_inspect_failed"
	ReasonSyncContainerStopped           = "sync_container_stopped"
	ReasonLogStreamEndedUnexpectedly     = "log_stream_ended_unexpectedly"
)
