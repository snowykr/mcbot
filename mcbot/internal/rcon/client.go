package rcon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gorcon/rcon"

	"github.com/snowy/mcbot/internal/logutil"
)

var (
	ErrConnectionFailed = errors.New("RCON 연결 실패")
	ErrAuthFailed       = errors.New("RCON 인증 실패")
	ErrTimeout          = errors.New("RCON 응답 시간 초과")
	ErrEmptyCommand     = errors.New("명령어가 비어있습니다")
)

type Client struct {
	address  string
	password string
	timeout  time.Duration
}

func NewClient(host string, port int, password string, timeout time.Duration) *Client {
	return &Client{
		address:  fmt.Sprintf("%s:%d", host, port),
		password: password,
		timeout:  timeout,
	}
}

func (c *Client) Execute(ctx context.Context, command string) (string, error) {
	if command == "" {
		return "", ErrEmptyCommand
	}

	deadline := c.timeout
	if ctxDeadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(ctxDeadline)
		if remaining < deadline {
			deadline = remaining
		}
	}

	if deadline <= 0 {
		return "", ErrTimeout
	}

	conn, err := rcon.Dial(c.address, c.password, rcon.SetDialTimeout(deadline), rcon.SetDeadline(deadline))
	if err != nil {
		if ctx.Err() != nil {
			return "", ErrTimeout
		}
		if errors.Is(err, rcon.ErrAuthFailed) {
			return "", ErrAuthFailed
		}
		return "", fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logutil.Debugf("[RCON] connection close error: %v", err)
		}
	}()

	response, err := conn.Execute(command)
	if err != nil {
		if ctx.Err() != nil {
			return "", ErrTimeout
		}
		return "", fmt.Errorf("명령 실행 실패: %w", err)
	}

	return response, nil
}
