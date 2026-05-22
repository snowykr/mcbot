package serverops

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/snowy/mcbot/internal/composectl"
	"github.com/snowy/mcbot/internal/dockerctl"
	"github.com/snowy/mcbot/internal/envfile"
)

const defaultStopTimeoutSeconds = 120

type DockerClient interface {
	InspectContainer(ctx context.Context, containerName string) (*dockerctl.ContainerState, error)
	StartContainer(ctx context.Context, containerName string) error
	StopContainer(ctx context.Context, containerName string, timeoutSeconds int) error
}

type defaultDockerClient struct{}

func (defaultDockerClient) InspectContainer(ctx context.Context, containerName string) (*dockerctl.ContainerState, error) {
	return dockerctl.InspectContainer(ctx, containerName)
}

func (defaultDockerClient) StartContainer(ctx context.Context, containerName string) error {
	return dockerctl.StartContainer(ctx, containerName)
}

func (defaultDockerClient) StopContainer(ctx context.Context, containerName string, timeoutSeconds int) error {
	return dockerctl.StopContainer(ctx, containerName, timeoutSeconds)
}

type ComposeClient interface {
	EnsureServiceCreated(ctx context.Context, paths composectl.Paths, service string, env map[string]string) error
}

type Options struct {
	Paths       composectl.Paths
	Docker      DockerClient
	Compose     ComposeClient
	IntentStore IntentStore
	Now         func() time.Time
}

type Result struct {
	Service   string   `json:"service"`
	Exists    bool     `json:"exists"`
	Running   bool     `json:"running"`
	Status    string   `json:"status"`
	Message   string   `json:"-"`
	Created   bool     `json:"-"`
	Started   bool     `json:"-"`
	Stopped   bool     `json:"-"`
	Container string   `json:"-"`
	Warnings  []string `json:"warnings,omitempty"`
}

func Start(ctx context.Context, opts Options) (Result, error) {
	opts, err := withDefaults(opts)
	if err != nil {
		return Result{}, err
	}
	service := composectl.MCServerService
	container, err := runtimeContainerName(opts.Paths)
	if err != nil {
		return Result{}, err
	}

	state, err := opts.Docker.InspectContainer(ctx, container)
	if err != nil {
		return Result{}, fmt.Errorf("inspect container %s: %w", container, err)
	}
	if state.Exists && state.Running {
		return statusResult(service, container, state, "server already running"), nil
	}

	created, err := ensureProvisionedForStart(ctx, opts, service, state)
	if err != nil {
		return Result{}, err
	}

	if err := opts.Docker.StartContainer(ctx, container); err != nil {
		return Result{}, fmt.Errorf("start container %s: %w", container, err)
	}
	after, err := opts.Docker.InspectContainer(ctx, container)
	if err != nil {
		return Result{}, fmt.Errorf("inspect container %s after start: %w", container, err)
	}
	result := statusResult(service, container, after, "server started")
	result.Created = created
	result.Started = true
	return result, nil
}

func Stop(ctx context.Context, opts Options) (Result, error) {
	opts, err := withDefaults(opts)
	if err != nil {
		return Result{}, err
	}
	service := composectl.MCServerService
	container, err := runtimeContainerName(opts.Paths)
	if err != nil {
		return Result{}, err
	}
	state, err := opts.Docker.InspectContainer(ctx, container)
	if err != nil {
		return Result{}, fmt.Errorf("inspect container %s: %w", container, err)
	}
	if !state.Exists || !state.Running {
		return statusResult(service, container, state, "server already stopped"), nil
	}

	stopTimeout := stopTimeoutSeconds(opts.Paths)
	intentRecorded := true
	var intentWarning string
	if err := opts.IntentStore.WriteStopIntent(ctx, NewStopIntent(service, container, opts.Now(), stopTimeout)); err != nil {
		intentRecorded = false
		intentWarning = fmt.Sprintf("stop intent not recorded: %v", err)
	}
	if err := opts.Docker.StopContainer(ctx, container, stopTimeout); err != nil {
		after, inspectErr := opts.Docker.InspectContainer(ctx, container)
		if intentRecorded && inspectErr != nil {
			if clearErr := opts.IntentStore.Clear(ctx); clearErr != nil {
				return Result{}, fmt.Errorf("stop container %s failed: %w; inspect after stop failed: %v; additionally clear stop intent failed: %v", container, err, inspectErr, clearErr)
			}
		}
		if intentRecorded && inspectErr == nil && after.Exists && after.Running {
			if clearErr := opts.IntentStore.Clear(ctx); clearErr != nil {
				return Result{}, fmt.Errorf("stop container %s failed: %w; additionally clear stop intent failed: %v", container, err, clearErr)
			}
		}
		return Result{}, fmt.Errorf("stop container %s: %w", container, err)
	}
	after, err := opts.Docker.InspectContainer(ctx, container)
	if err != nil {
		return Result{}, fmt.Errorf("inspect container %s after stop: %w", container, err)
	}
	result := statusResult(service, container, after, "server stopped")
	result.Stopped = true
	if intentWarning != "" {
		result.Warnings = append(result.Warnings, intentWarning)
	}
	return result, nil
}

func Status(ctx context.Context, opts Options) (Result, error) {
	opts, err := withDefaults(opts)
	if err != nil {
		return Result{}, err
	}
	container, err := runtimeContainerName(opts.Paths)
	if err != nil {
		return Result{}, err
	}
	state, err := opts.Docker.InspectContainer(ctx, container)
	if err != nil {
		return Result{}, fmt.Errorf("inspect container %s: %w", container, err)
	}
	return statusResult(composectl.MCServerService, container, state, ""), nil
}

func withDefaults(opts Options) (Options, error) {
	if opts.Paths.RepoRoot == "" {
		paths, err := composectl.DiscoverRepoRoot("")
		if err != nil {
			return Options{}, err
		}
		opts.Paths = paths
	}
	if opts.Docker == nil {
		opts.Docker = defaultDockerClient{}
	}
	if opts.Compose == nil {
		opts.Compose = composectl.Client{}
	}
	if opts.IntentStore == nil {
		opts.IntentStore = NewFileIntentStore(CLIDataDir(opts.Paths.RepoRoot))
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts, nil
}

func statusResult(service, container string, state *dockerctl.ContainerState, message string) Result {
	result := Result{Service: service, Container: container, Message: message}
	if state == nil {
		result.Status = "unknown"
		return result
	}
	result.Exists = state.Exists
	result.Running = state.Running
	result.Status = state.Status
	if !state.Exists {
		result.Status = "missing"
	} else if result.Status == "" {
		if state.Running {
			result.Status = "running"
		} else {
			result.Status = "stopped"
		}
	}
	return result
}

func runtimeContainerName(paths composectl.Paths) (string, error) {
	env, err := envfile.Load(paths.EnvFile)
	if err != nil {
		return "", fmt.Errorf("load env file for container name: %w", err)
	}
	if name, ok := env.Values["MC_CONTAINER_NAME"]; ok {
		if name != "" {
			return name, nil
		}
		return composectl.MCServerService, nil
	}
	if name := os.Getenv("MC_CONTAINER_NAME"); name != "" {
		return name, nil
	}
	return composectl.MCServerService, nil
}

func ensureProvisionedForStart(ctx context.Context, opts Options, service string, state *dockerctl.ContainerState) (bool, error) {
	if state.Running {
		return false, nil
	}
	cfg, err := LoadSources(Sources{EnvFile: opts.Paths.EnvFile, ConfigFile: opts.Paths.ConfigFile})
	if err != nil {
		return false, err
	}
	if err := opts.Compose.EnsureServiceCreated(ctx, opts.Paths, service, ComposeEnvironment(cfg)); err != nil {
		return false, fmt.Errorf("ensure service %s: %w", service, err)
	}
	return !state.Exists, nil
}

func stopTimeoutSeconds(paths composectl.Paths) int {
	env, err := envfile.Load(paths.EnvFile)
	if err == nil {
		if value, ok := env.Values["STOP_TIMEOUT_SECONDS"]; ok {
			return parseStopTimeoutSeconds(value)
		}
	}
	return parseStopTimeoutSeconds(os.Getenv("STOP_TIMEOUT_SECONDS"))
}

func parseStopTimeoutSeconds(value string) int {
	if value == "" {
		return defaultStopTimeoutSeconds
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return defaultStopTimeoutSeconds
	}
	return parsed
}
