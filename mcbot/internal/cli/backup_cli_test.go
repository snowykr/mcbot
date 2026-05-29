package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/snowy/mcbot/internal/composectl"
	"github.com/snowy/mcbot/internal/dockerctl"
	"github.com/snowy/mcbot/internal/mcconfig"
	"github.com/snowy/mcbot/internal/serverops"
)

type mapDockerStates map[string]*dockerctl.ContainerState

func (d mapDockerStates) InspectContainer(_ context.Context, containerName string) (*dockerctl.ContainerState, error) {
	if state, ok := d[containerName]; ok {
		return state, nil
	}
	return &dockerctl.ContainerState{Exists: false}, nil
}

func (d mapDockerStates) StartContainer(context.Context, string) error  { return nil }
func (d mapDockerStates) StopContainer(context.Context, string, int) error { return nil }

func writeBackupTestRepo(t *testing.T, repoRoot, containerName string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repoRoot, "mcbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "mcbot", "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := "MC_CONTAINER_NAME=" + containerName + "\n"
	if err := os.WriteFile(filepath.Join(repoRoot, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(repoRoot, "mc-server.toml")
	if err := mcconfig.Write(configFile, mcconfig.Defaults()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "data", "minecraft"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBackupCreatePreconditionsUseResolvedRepoServerStatus(t *testing.T) {
	root := t.TempDir()
	repoA := filepath.Join(root, "repo-a")
	repoB := filepath.Join(root, "repo-b")
	writeBackupTestRepo(t, repoA, "mc-a")
	writeBackupTestRepo(t, repoB, "mc-b")

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoA); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	docker := mapDockerStates{
		"mc-a": {Exists: true, Running: false, Status: "exited"},
		"mc-b": {Exists: true, Running: true, Status: "running"},
	}
	defer SetBackupServerStatusForTest(func(ctx context.Context, paths backupPaths) (serverops.Result, error) {
		result, err := serverops.Status(ctx, serverops.Options{
			Paths:  composePathsFromBackup(paths),
			Docker: docker,
		})
		if err != nil {
			return serverops.Result{}, err
		}
		if result.Container != "mc-b" {
			t.Fatalf("status container = %q, want mc-b (resolved repo B)", result.Container)
		}
		return result, nil
	})()

	paths, err := resolveBackupPaths(backupFlags{configPath: filepath.Join(repoB, "mc-server.toml")})
	if err != nil {
		t.Fatalf("resolveBackupPaths: %v", err)
	}
	if paths.repoRoot != repoB {
		t.Fatalf("repoRoot = %q, want %q", paths.repoRoot, repoB)
	}

	stopped, quiescer, err := backupCreatePreconditions(context.Background(), paths)
	if err == nil && stopped && quiescer == nil {
		t.Fatal("preconditions treated target server as stopped while mc-b is running")
	}
}

func TestRestoreStoppedPreconditionUsesResolvedRepoServerStatus(t *testing.T) {
	root := t.TempDir()
	repoA := filepath.Join(root, "repo-a")
	repoB := filepath.Join(root, "repo-b")
	writeBackupTestRepo(t, repoA, "mc-a")
	writeBackupTestRepo(t, repoB, "mc-b")

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoA); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	docker := mapDockerStates{
		"mc-a": {Exists: true, Running: false, Status: "exited"},
		"mc-b": {Exists: true, Running: true, Status: "running"},
	}
	defer SetBackupServerStatusForTest(func(ctx context.Context, paths backupPaths) (serverops.Result, error) {
		return serverops.Status(ctx, serverops.Options{
			Paths:  composePathsFromBackup(paths),
			Docker: docker,
		})
	})()

	paths, err := resolveBackupPaths(backupFlags{configPath: filepath.Join(repoB, "mc-server.toml")})
	if err != nil {
		t.Fatalf("resolveBackupPaths: %v", err)
	}

	stopped, err := restoreStoppedPrecondition(context.Background(), paths)
	if err != nil {
		t.Fatalf("restoreStoppedPrecondition: %v", err)
	}
	if stopped {
		t.Fatal("restore precondition passed while target repo server is running")
	}
}

func TestComposePathsFromBackupMatchesResolvedRepo(t *testing.T) {
	paths := backupPaths{
		repoRoot:   "/tmp/repo-b",
		envFile:    "/tmp/repo-b/.env",
		configPath: "/tmp/repo-b/mc-server.toml",
	}
	got := composePathsFromBackup(paths)
	want := composectl.Paths{
		RepoRoot:    "/tmp/repo-b",
		EnvFile:     "/tmp/repo-b/.env",
		ConfigFile:  "/tmp/repo-b/mc-server.toml",
		ComposeFile: "/tmp/repo-b/docker-compose.yml",
	}
	if got != want {
		t.Fatalf("compose paths = %#v, want %#v", got, want)
	}
}
