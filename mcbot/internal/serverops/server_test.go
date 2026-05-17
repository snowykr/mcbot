package serverops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/composectl"
	"github.com/snowy/mcbot/internal/dockerctl"
)

type fakeDocker struct {
	states       []*dockerctl.ContainerState
	inspects     []string
	starts       []string
	stopNames    []string
	stops        []int
	inspectErr   error
	startErr     error
	stopErr      error
	inspectCalls int
}

func (d *fakeDocker) InspectContainer(_ context.Context, containerName string) (*dockerctl.ContainerState, error) {
	d.inspectCalls++
	d.inspects = append(d.inspects, containerName)
	if d.inspectErr != nil {
		return nil, d.inspectErr
	}
	if len(d.states) == 0 {
		return &dockerctl.ContainerState{Exists: false}, nil
	}
	state := d.states[0]
	if len(d.states) > 1 {
		d.states = d.states[1:]
	}
	return state, nil
}

func (d *fakeDocker) StartContainer(_ context.Context, containerName string) error {
	d.starts = append(d.starts, containerName)
	return d.startErr
}

func (d *fakeDocker) StopContainer(_ context.Context, containerName string, timeoutSeconds int) error {
	d.stopNames = append(d.stopNames, containerName)
	d.stops = append(d.stops, timeoutSeconds)
	return d.stopErr
}

type fakeCompose struct {
	calls int
	env   map[string]string
	err   error
}

func (c *fakeCompose) EnsureServiceCreated(_ context.Context, _ composectl.Paths, _ string, env map[string]string) error {
	c.calls++
	c.env = env
	return c.err
}

func TestServerStartEnsuresContainer(t *testing.T) {
	dir := t.TempDir()
	paths := writeServerOpsFiles(t, dir, "MC_CONTAINER_NAME=snowy-mc\n", "")
	docker := &fakeDocker{states: []*dockerctl.ContainerState{
		{Exists: false},
		{Exists: true, Running: true, Status: "running"},
	}}
	compose := &fakeCompose{}

	result, err := Start(context.Background(), Options{Paths: paths, Docker: docker, Compose: compose})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if compose.calls != 1 {
		t.Fatalf("compose calls = %d, want 1", compose.calls)
	}
	if got := len(docker.starts); got != 1 {
		t.Fatalf("docker starts = %d, want 1", got)
	}
	if result.Service != composectl.MCServerService || !result.Exists || !result.Running || result.Status != "running" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestServerStartEnsuresStoppedContainerThroughComposeBridge(t *testing.T) {
	dir := t.TempDir()
	paths := writeServerOpsFiles(t, dir, "MC_CONTAINER_NAME=snowy-mc\n", "")
	docker := &fakeDocker{states: []*dockerctl.ContainerState{
		{Exists: true, Running: false, Status: "exited"},
		{Exists: true, Running: true, Status: "running"},
	}}
	compose := &fakeCompose{}

	result, err := Start(context.Background(), Options{Paths: paths, Docker: docker, Compose: compose})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if compose.calls != 1 {
		t.Fatalf("compose calls = %d, want 1 for stopped container", compose.calls)
	}
	if got := compose.env["VERSION"]; got == "" {
		t.Fatalf("compose env VERSION is empty; stopped start did not load TOML/default compose environment: %#v", compose.env)
	}
	if got := len(docker.starts); got != 1 {
		t.Fatalf("docker starts = %d, want 1", got)
	}
	if result.Created {
		t.Fatalf("Created = true, want false for existing stopped container")
	}
	if !result.Exists || !result.Running || result.Status != "running" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestServerStopIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	paths := writeServerOpsFiles(t, dir, "", "")
	store := NewFileIntentStore(filepath.Join(dir, "data", "mcbot"))
	docker := &fakeDocker{states: []*dockerctl.ContainerState{{Exists: true, Running: false, Status: "exited"}}}

	result, err := Stop(context.Background(), Options{Paths: paths, Docker: docker, IntentStore: store})
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if len(docker.stops) != 0 {
		t.Fatalf("docker stop called for stopped container")
	}
	if _, found, err := store.Load(context.Background()); err != nil || found {
		t.Fatalf("stop intent found=%v err=%v, want no intent", found, err)
	}
	if result.Message != "server already stopped" {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestServerStatusUsesDockerInspection(t *testing.T) {
	dir := t.TempDir()
	paths := writeServerOpsFiles(t, dir, "MC_CONTAINER_NAME=snowy-mc\n", "")
	docker := &fakeDocker{states: []*dockerctl.ContainerState{{Exists: true, Running: true, Status: "running"}}}

	result, err := Status(context.Background(), Options{Paths: paths, Docker: docker})
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if docker.inspectCalls != 1 {
		t.Fatalf("inspect calls = %d, want 1", docker.inspectCalls)
	}
	if result.Service != composectl.MCServerService || !result.Exists || !result.Running || result.Status != "running" {
		t.Fatalf("unexpected status: %+v", result)
	}
}

func TestCliStopDoesNotTriggerFalseCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	paths := writeServerOpsFiles(t, dir, "STOP_TIMEOUT_SECONDS=5\n", "")
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	store := NewFileIntentStore(filepath.Join(dir, "data", "mcbot"))
	docker := &fakeDocker{states: []*dockerctl.ContainerState{
		{Exists: true, Running: true, Status: "running"},
		{Exists: true, Running: false, Status: "exited"},
	}}

	if _, err := Stop(context.Background(), Options{Paths: paths, Docker: docker, IntentStore: store, Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	matched, err := store.HasUnexpiredStopIntent(context.Background(), composectl.MCServerService, now.Add(10*time.Second))
	if err != nil {
		t.Fatalf("HasUnexpiredStopIntent failed: %v", err)
	}
	if !matched {
		t.Fatal("CLI stop intent expired or missing before bot could observe it")
	}
	matched, err = store.ConsumeUnexpiredStopIntent(context.Background(), composectl.MCServerService, now.Add(10*time.Second))
	if err != nil {
		t.Fatalf("ConsumeUnexpiredStopIntent failed: %v", err)
	}
	if !matched {
		t.Fatal("CLI stop intent was not consumed by bot-side observation")
	}
	matched, err = store.HasUnexpiredStopIntent(context.Background(), composectl.MCServerService, now.Add(11*time.Second))
	if err != nil {
		t.Fatalf("HasUnexpiredStopIntent after consume failed: %v", err)
	}
	if matched {
		t.Fatal("CLI stop intent matched more than once after bot-side observation")
	}
}

func TestServerStopClearsIntentWhenStopFailsAndContainerStillRuns(t *testing.T) {
	dir := t.TempDir()
	paths := writeServerOpsFiles(t, dir, "", "")
	store := NewFileIntentStore(filepath.Join(dir, "data", "mcbot"))
	docker := &fakeDocker{stopErr: errors.New("boom"), states: []*dockerctl.ContainerState{
		{Exists: true, Running: true, Status: "running"},
		{Exists: true, Running: true, Status: "running"},
	}}

	if _, err := Stop(context.Background(), Options{Paths: paths, Docker: docker, IntentStore: store}); err == nil {
		t.Fatal("Stop succeeded, want error")
	}
	if _, found, err := store.Load(context.Background()); err != nil || found {
		t.Fatalf("stop intent found=%v err=%v, want cleared intent", found, err)
	}
}

func TestServerStopHonorsEnvStopTimeoutWhenTomlIsInvalid(t *testing.T) {
	dir := t.TempDir()
	paths := writeServerOpsFiles(t, dir, "MC_CONTAINER_NAME=snowy-mc\nSTOP_TIMEOUT_SECONDS=5\n", "[server\nversion = \"1.20.1\"\n")
	store := NewFileIntentStore(filepath.Join(dir, "data", "mcbot"))
	now := time.Date(2026, 5, 17, 6, 0, 0, 0, time.UTC)
	docker := &fakeDocker{states: []*dockerctl.ContainerState{{Exists: true, Running: true, Status: "running"}, {Exists: true, Running: false, Status: "exited"}}}

	result, err := Stop(context.Background(), Options{Paths: paths, Docker: docker, IntentStore: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if result.Container != "snowy-mc" {
		t.Fatalf("result.Container = %q, want snowy-mc", result.Container)
	}
	if len(docker.stops) != 1 {
		t.Fatalf("docker stops = %v, want [5]", docker.stops)
	}
	if docker.stops[0] != 5 {
		t.Fatalf("docker stop timeout = %d, want 5", docker.stops[0])
	}
	intent, found, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load stop intent failed: %v", err)
	}
	if !found {
		t.Fatal("stop intent not found after Stop")
	}
	wantExpiresAt := now.Add(5*time.Second + StopIntentBuffer)
	if !intent.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("intent.ExpiresAt = %v, want %v", intent.ExpiresAt, wantExpiresAt)
	}
}

func TestServerCommandsHonorEnvContainerNameWhenTomlIsInvalid(t *testing.T) {
	tests := []struct {
		name          string
		run           func(t *testing.T, paths composectl.Paths, docker *fakeDocker) Result
		wantInspects  []string
		wantStarts    []string
		wantStopNames []string
		wantContainer string
	}{
		{
			name: "status",
			run: func(t *testing.T, paths composectl.Paths, docker *fakeDocker) Result {
				t.Helper()
				result, err := Status(context.Background(), Options{Paths: paths, Docker: docker})
				if err != nil {
					t.Fatalf("Status failed: %v", err)
				}
				return result
			},
			wantInspects:  []string{"snowy-mc"},
			wantContainer: "snowy-mc",
		},
		{
			name: "stop",
			run: func(t *testing.T, paths composectl.Paths, docker *fakeDocker) Result {
				t.Helper()
				result, err := Stop(context.Background(), Options{Paths: paths, Docker: docker, IntentStore: NewFileIntentStore(filepath.Join(paths.RepoRoot, "data", "mcbot"))})
				if err != nil {
					t.Fatalf("Stop failed: %v", err)
				}
				return result
			},
			wantInspects:  []string{"snowy-mc", "snowy-mc"},
			wantStopNames: []string{"snowy-mc"},
			wantContainer: "snowy-mc",
		},
		{
			name: "start already running",
			run: func(t *testing.T, paths composectl.Paths, docker *fakeDocker) Result {
				t.Helper()
				result, err := Start(context.Background(), Options{Paths: paths, Docker: docker, Compose: &fakeCompose{}})
				if err != nil {
					t.Fatalf("Start failed: %v", err)
				}
				return result
			},
			wantInspects:  []string{"snowy-mc"},
			wantContainer: "snowy-mc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := writeServerOpsFiles(t, dir, "MC_CONTAINER_NAME=snowy-mc\n", "[server\nversion = \"1.20.1\"\n")
			docker := &fakeDocker{}
			switch tt.name {
			case "status":
				docker.states = []*dockerctl.ContainerState{{Exists: true, Running: true, Status: "running"}}
			case "stop":
				docker.states = []*dockerctl.ContainerState{{Exists: true, Running: true, Status: "running"}, {Exists: true, Running: false, Status: "exited"}}
			case "start already running":
				docker.states = []*dockerctl.ContainerState{{Exists: true, Running: true, Status: "running"}}
			}

			result := tt.run(t, paths, docker)

			if result.Container != tt.wantContainer {
				t.Fatalf("result.Container = %q, want %q", result.Container, tt.wantContainer)
			}
			if len(docker.inspects) != len(tt.wantInspects) {
				t.Fatalf("inspect names = %v, want %v", docker.inspects, tt.wantInspects)
			}
			for i, want := range tt.wantInspects {
				if docker.inspects[i] != want {
					t.Fatalf("inspect[%d] = %q, want %q", i, docker.inspects[i], want)
				}
			}
			if len(docker.starts) != len(tt.wantStarts) {
				t.Fatalf("start names = %v, want %v", docker.starts, tt.wantStarts)
			}
			for i, want := range tt.wantStarts {
				if docker.starts[i] != want {
					t.Fatalf("start[%d] = %q, want %q", i, docker.starts[i], want)
				}
			}
			if len(docker.stopNames) != len(tt.wantStopNames) {
				t.Fatalf("stop names = %v, want %v", docker.stopNames, tt.wantStopNames)
			}
			for i, want := range tt.wantStopNames {
				if docker.stopNames[i] != want {
					t.Fatalf("stop[%d] = %q, want %q", i, docker.stopNames[i], want)
				}
			}
		})
	}
}

func TestServerStartFromStoppedContainerStillRequiresValidTomlForProvisioning(t *testing.T) {
	dir := t.TempDir()
	paths := writeServerOpsFiles(t, dir, "MC_CONTAINER_NAME=snowy-mc\n", "[server\nversion = \"1.20.1\"\n")
	docker := &fakeDocker{states: []*dockerctl.ContainerState{{Exists: true, Running: false, Status: "exited"}}}
	compose := &fakeCompose{}

	_, err := Start(context.Background(), Options{Paths: paths, Docker: docker, Compose: compose})
	if err == nil {
		t.Fatal("Start succeeded, want TOML load error")
	}
	if len(docker.inspects) != 1 || docker.inspects[0] != "snowy-mc" {
		t.Fatalf("inspect names = %v, want [snowy-mc]", docker.inspects)
	}
	if got := len(docker.starts); got != 0 {
		t.Fatalf("docker starts = %d, want 0", got)
	}
	if compose.calls != 0 {
		t.Fatalf("compose calls = %d, want 0 before TOML load succeeds", compose.calls)
	}
}

func writeServerOpsFiles(t *testing.T, dir, envContents, configContents string) composectl.Paths {
	t.Helper()
	paths := composectl.DefaultPaths(dir)
	if err := os.WriteFile(paths.EnvFile, []byte(envContents), 0o600); err != nil {
		t.Fatalf("write env failed: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(configContents), 0o600); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	if err := os.WriteFile(paths.ComposeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write compose failed: %v", err)
	}
	return paths
}
