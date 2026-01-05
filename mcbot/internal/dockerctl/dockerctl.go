package dockerctl

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ContainerState struct {
	Exists    bool
	Running   bool
	StartedAt time.Time
}

func InspectContainer(ctx context.Context, containerName string) (*ContainerState, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"-f", "{{.State.Running}}|{{.State.StartedAt}}",
		containerName)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.ToLower(string(exitErr.Stderr))
			if strings.Contains(stderr, "no such object") ||
				strings.Contains(stderr, "no such container") ||
				strings.Contains(stderr, "error: no such") {
				return &ContainerState{Exists: false}, nil
			}
		}
		return nil, fmt.Errorf("docker inspect failed: %w", err)
	}

	parts := strings.Split(strings.TrimSpace(string(output)), "|")
	if len(parts) != 2 {
		return nil, fmt.Errorf("unexpected docker inspect output: %s", output)
	}

	running := parts[0] == "true"
	startedAt, _ := time.Parse(time.RFC3339Nano, parts[1])

	return &ContainerState{
		Exists:    true,
		Running:   running,
		StartedAt: startedAt,
	}, nil
}

func StartContainer(ctx context.Context, containerName string) error {
	cmd := exec.CommandContext(ctx, "docker", "start", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker start failed: %w, output: %s", err, string(output))
	}
	return nil
}

func StopContainer(ctx context.Context, containerName string, timeoutSeconds int) error {
	cmd := exec.CommandContext(ctx, "docker", "stop",
		"--time", strconv.Itoa(timeoutSeconds),
		containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker stop failed: %w, output: %s", err, string(output))
	}
	return nil
}

type LogLine struct {
	Text string
	Err  error
}

func FollowLogs(ctx context.Context, containerName string, since time.Time) <-chan LogLine {
	ch := make(chan LogLine, 100)

	go func() {
		var wg sync.WaitGroup
		var closeOnce sync.Once
		closed := make(chan struct{})

		safeSend := func(line LogLine) bool {
			select {
			case <-closed:
				return false
			default:
			}
			select {
			case ch <- line:
				return true
			case <-closed:
				return false
			}
		}

		safeClose := func() {
			closeOnce.Do(func() {
				close(closed)
			})
		}

		defer func() {
			safeClose()
			wg.Wait()
			close(ch)
		}()

		sinceStr := since.Format(time.RFC3339)
		cmd := exec.CommandContext(ctx, "docker", "logs",
			"-f", "--since", sinceStr, containerName)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			safeSend(LogLine{Err: fmt.Errorf("failed to get stdout pipe: %w", err)})
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			safeSend(LogLine{Err: fmt.Errorf("failed to get stderr pipe: %w", err)})
			return
		}

		if err := cmd.Start(); err != nil {
			safeSend(LogLine{Err: fmt.Errorf("failed to start docker logs: %w", err)})
			return
		}

		cmdDone := make(chan struct{})
		ctxCancelled := make(chan struct{})

		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				select {
				case <-closed:
					return
				default:
				}
				if !safeSend(LogLine{Text: scanner.Text()}) {
					return
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				select {
				case <-closed:
					return
				default:
				}
				if !safeSend(LogLine{Text: scanner.Text()}) {
					return
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(cmdDone)

			waitErr := cmd.Wait()

			select {
			case <-ctxCancelled:
				return
			default:
			}

			if waitErr != nil {
				safeSend(LogLine{Err: fmt.Errorf("docker logs process exited: %w", waitErr)})
			}
		}()

		select {
		case <-ctx.Done():
			close(ctxCancelled)
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-cmdDone
		case <-cmdDone:
		}

		safeClose()
	}()

	return ch
}
