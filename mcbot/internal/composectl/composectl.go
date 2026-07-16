package composectl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const MCServerService = "mc-server"

type Paths struct {
	RepoRoot    string
	EnvFile     string
	ConfigFile  string
	ComposeFile string
}

type Invocation struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

type Runner interface {
	Run(ctx context.Context, inv Invocation) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	cmd := exec.CommandContext(ctx, inv.Name, inv.Args...)
	cmd.Dir = inv.Dir
	cmd.Env = inv.Env
	return cmd.CombinedOutput()
}

type Client struct {
	Runner Runner
}

func DiscoverRepoRoot(start string) (Paths, error) {
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return Paths{}, fmt.Errorf("get working directory: %w", err)
		}
	}
	absStart, err := filepath.Abs(start)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve start directory: %w", err)
	}
	current := absStart
	for {
		composePath := filepath.Join(current, "docker-compose.yml")
		goModPath := filepath.Join(current, "mcbot", "go.mod")
		if fileExists(composePath) && fileExists(goModPath) {
			return DefaultPaths(current), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return Paths{}, fmt.Errorf("repository root not found from %s", absStart)
		}
		current = parent
	}
}

func DefaultPaths(repoRoot string) Paths {
	repoRoot = filepath.Clean(repoRoot)
	return Paths{
		RepoRoot:    repoRoot,
		EnvFile:     filepath.Join(repoRoot, ".env"),
		ConfigFile:  filepath.Join(repoRoot, "mc-server.toml"),
		ComposeFile: filepath.Join(repoRoot, "docker-compose.yml"),
	}
}

func (c Client) EnsureServiceCreated(ctx context.Context, paths Paths, service string, env map[string]string) error {
	runner := c.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	inv := EnsureServiceCreateInvocation(paths, service, env)
	output, err := runner.Run(ctx, inv)
	if err != nil {
		return fmt.Errorf("docker compose create %s failed: %w, output: %s", service, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func EnsureServiceCreateInvocation(paths Paths, service string, env map[string]string) Invocation {
	args := []string{"compose", "--project-directory", paths.RepoRoot}
	if fileExists(paths.EnvFile) {
		args = append(args, "--env-file", paths.EnvFile)
	}
	args = append(args, "-f", paths.ComposeFile, "create", service)

	return Invocation{
		Name: "docker",
		Args: args,
		Dir:  paths.RepoRoot,
		Env:  mergeEnv(os.Environ(), env),
	}
}

func mergeEnv(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, replace := overrides[key]; replace {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
