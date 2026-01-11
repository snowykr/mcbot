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
make test-race          # go test -race ./...

# Integration tests (require Docker)
make test-integration   # -tags=integration
make test-all           # test-race + test-integration

# Run a SINGLE test
cd mcbot && go test -v -run TestControllerStart_FromStoppedState ./internal/mcserver/...
cd mcbot && go test -v -run TestIntegration_ -tags=integration ./internal/mcserver/...

# Run tests in a specific package
cd mcbot && go test -v ./internal/discord/...
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

### Type Definitions
```go
type ServerState int

const (
    StateStopped ServerState = iota
    StateStarting
    StateRunning
)

func (s ServerState) String() string { ... }
func (s ServerState) Korean() string { ... }  // Korean localization
```

### Error Handling
- Return `error` as last return value
- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`
- Use custom error types for state transitions (`TransitionError`)

### Concurrency Patterns
- `sync.Mutex` / `sync.RWMutex` for shared state
- Channels for async results (`<-chan StartResult`)
- `context.Context` for cancellation
- `sync.WaitGroup` for goroutine coordination

```go
// Async operation pattern
func (c *Controller) Start(ctx context.Context) <-chan StartResult {
    resultCh := make(chan StartResult, 1)
    go func() {
        defer close(resultCh)
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

### Docker Interaction
- Uses `exec.CommandContext` with docker CLI (not Docker SDK)
- Container inspection via `docker inspect` with format templates
- Log following via `docker logs -f --since`

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

## Don'ts
- Don't use Docker SDK - this project uses CLI for simplicity
- Don't suppress errors with `_ = err`
- Don't use global state - pass dependencies via constructors
- Don't block the main goroutine - use async patterns
- Don't skip `defer controller.Shutdown()` in tests
