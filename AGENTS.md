# AGENTS.md - MCBot Discord Bot

> Guidelines for AI agents working on this Go-based Discord bot that manages Minecraft servers via Docker.

## Project Overview

MCBot is a Discord bot written in Go that controls a Minecraft server container. It provides Discord button-based server start/stop controls, real-time state monitoring, player tracking via log parsing, and auto-recovery from crashes.

## Build & Test Commands

```bash
# Build
make go-build           # Builds mcbot/cmd/mcbot -> mcbot/mcbot
make go-deps            # Runs go mod tidy

# Docker Compose
make up                 # Build and start mcbot container
make up-all             # Start all services (mcbot + mc-server)
make down               # Stop containers
make nuke               # Remove containers + volumes

# Unit tests
make test               # go test ./...
make test-race          # go test -race ./... (ALWAYS run before PR)

# Integration tests (require Docker, ~5-10 min)
make test-integration   # -tags=integration
make test-all           # test-race + test-integration

# Run a SINGLE test
cd mcbot && go test -v -run TestControllerStart_FromStoppedState ./internal/mcserver/...
cd mcbot && go test -v -run TestIntegration_ -tags=integration ./internal/mcserver/...

# Run tests in a specific package
cd mcbot && go test -v ./internal/discord/...
cd mcbot && go test -v ./internal/mcserver/...

# Clean up stale test containers
make test-integration-clean
```

## Project Structure

```
mcbot/
  cmd/mcbot/           # Main entrypoint
  internal/
    config/            # Environment-based configuration
    discord/           # Discord interaction handlers, embeds
    dockerctl/         # Docker CLI wrapper (exec-based, not SDK)
    logutil/           # Debug/info logging utilities
    mcserver/          # Server controller, log multiplexer, player tracker
    state/             # State machine (ServerState enum + Manager)
```

## Code Style Guidelines

### Language & Runtime
- **Go 1.25+** - Target: Linux containers (alpine-based)
- Dependencies: minimal (only discordgo + stdlib)

### Import Order
```go
import (
    "context"           // 1. Standard library
    "fmt"

    "github.com/bwmarrin/discordgo"  // 2. External packages

    "github.com/snowy/mcbot/internal/config"  // 3. Internal packages
)
```

### Naming Conventions
- **Files**: lowercase with underscores (`controller_start_test.go`)
- **Packages**: short, lowercase, no underscores
- **Types/Constants**: PascalCase (`ServerState`, `StateStopped`)
- **Test functions**: `Test<Type>_<Scenario>` (e.g., `TestControllerStart_FromStoppedState`)
- **Interfaces**: Define at the consumer, not provider (see `ServerController` in handler.go)

### Error Handling
- Return `error` as last return value
- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- Use custom error types for state transitions (`TransitionError`)
- Never suppress errors with `_ = err`

### Concurrency Patterns
- `sync.Mutex` / `sync.RWMutex` for shared state
- Channels for async results (`<-chan StartResult`)
- `context.Context` for cancellation
- `sync.WaitGroup` for goroutine coordination

```go
// Async operation pattern (standard in this codebase)
func (c *Controller) Start(_ context.Context) <-chan StartResult {
    resultCh := make(chan StartResult, 1)
    go func() {
        defer close(resultCh)
        // ... operation logic
        resultCh <- result
    }()
    return resultCh
}
```

### Logging
```go
log.Printf("Standard log")
logutil.Infof("[RUNTIME] Important event")  // Always logged
logutil.Debugf("[STATE] Debug info")        // Only when MCBOT_DEBUG=true
```

### Testing
- Unit tests: `*_test.go` without build tags
- Integration tests: `//go:build integration` tag
- Always `defer controller.Shutdown()` after creating controllers
- Use `-race` flag to detect race conditions

### Configuration
All config via environment variables with defaults in code:
- `DISCORD_TOKEN` (required)
- `MC_CONTAINER_NAME` (default: "mc-server")
- `READY_TIMEOUT_SECONDS` (default: 600)
- `MCBOT_DEBUG` (true/false for debug logs)

## State Machine

```
Stopped/Error/Crashed -> Starting (on start)
Starting -> Running (on ready) | Crashed (on failure)
Running -> Stopping (on stop) | Crashed (on unexpected stop)
Stopping -> Stopped (on success)
```

### Crash Reason Constants
All crash reasons are defined in `internal/mcserver/crash_reason.go`:
```go
const (
    ReasonRuntimeContainerStopped        = "runtime_container_stopped"
    ReasonRuntimeInspectFailedRepeatedly = "runtime_inspect_failed_repeatedly"
    ReasonRuntimeFailurePrefix           = "runtime_failure_"
    ReasonRuntimeNormalShutdown          = "runtime_normal_shutdown"
    ReasonSyncDetectedUnexpectedStop     = "sync_detected_unexpected_stop"
    // ... see file for full list
)
```
**Rule**: Always use these constants, never string literals.

## Key Architecture Decisions

### Context Lifecycle Separation
- Discord interaction context and server operation context are fully separated
- `Controller.Start/Stop` ignore external context and use `context.Background()`
- This ensures server operations continue even if Discord request times out

### LogMultiplexer Lifecycle
- Subscriber channels persist across Start/Stop cycles (only closed on Close)
- Stop() cancels the current FollowLogs but keeps channels open for reconnection
- Close() is called only in Controller.Shutdown()

### Shutdown Intent Detection
- Uses `shutdownIntentFromInside` atomic bool to distinguish crash vs graceful shutdown
- Container watcher waits for log watcher to finish (via `logWatcherDoneCh`) before deciding
- Grace period (`ShutdownIntentGracePeriod = 2s`) prevents false crash detection

## Don'ts

- Don't use Docker SDK - this project uses CLI for simplicity
- Don't suppress errors with `_ = err`
- Don't use global state - pass dependencies via constructors
- Don't block the main goroutine - use async patterns
- Don't skip `defer controller.Shutdown()` in tests
- Don't use string literals for crash reasons - use constants from `crash_reason.go`
