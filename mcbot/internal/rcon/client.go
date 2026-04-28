package rcon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	gorcon "github.com/gorcon/rcon"

	"github.com/snowy/mcbot/internal/logutil"
)

var (
	ErrConnectionFailed = errors.New("RCON 연결 실패")
	ErrAuthFailed       = errors.New("RCON 인증 실패")
	ErrTimeout          = errors.New("RCON 응답 시간 초과")
	ErrCanceled         = errors.New("RCON 요청이 취소되었습니다")
	ErrEmptyCommand     = errors.New("명령어가 비어있습니다")
)

type Client struct {
	address  string
	password string
	timeout  time.Duration
}

func NewClient(host string, port int, password string, timeout time.Duration) *Client {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	return &Client{
		address:  net.JoinHostPort(host, strconv.Itoa(port)),
		password: password,
		timeout:  timeout,
	}
}

func (c *Client) Execute(ctx context.Context, command string) (string, error) {
	if command == "" {
		return "", ErrEmptyCommand
	}
	if err := mapContextError(ctx.Err()); err != nil {
		return "", err
	}

	deadline := effectiveOperationDeadline(ctx, c.timeout)
	if deadline <= 0 {
		return "", ErrTimeout
	}

	conn, stopContextWatcher, err := c.dial(ctx, deadline)
	if err != nil {
		if mappedErr := mapContextError(ctx.Err()); mappedErr != nil {
			return "", mappedErr
		}
		if errors.Is(err, gorcon.ErrAuthFailed) {
			return "", ErrAuthFailed
		}
		if mappedErr := mapTimeoutError(err); mappedErr != nil {
			return "", mappedErr
		}
		return "", fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logutil.Debugf("[RCON] connection close error: %v", err)
		}
	}()
	defer stopContextWatcher()

	response, err := conn.Execute(command)
	if err != nil {
		if mappedErr := mapContextError(ctx.Err()); mappedErr != nil {
			return "", mappedErr
		}
		if mappedErr := mapTimeoutError(err); mappedErr != nil {
			return "", mappedErr
		}
		return "", fmt.Errorf("명령 실행 실패: %w", err)
	}

	return response, nil
}

func effectiveOperationDeadline(ctx context.Context, fallback time.Duration) time.Duration {
	deadline := fallback
	if ctxDeadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(ctxDeadline)
		if remaining < deadline {
			deadline = remaining
		}
	}
	return deadline
}

func (c *Client) dial(ctx context.Context, deadline time.Duration) (*gorcon.Conn, func(), error) {
	dialer := net.Dialer{Timeout: deadline}
	rawConn, err := dialer.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return nil, nil, fmt.Errorf("rcon: %w", err)
	}

	stopContextWatcher := closeConnOnContextDone(ctx, rawConn)
	conn, err := gorcon.Open(rawConn, c.password, gorcon.SetDeadline(deadline))
	if err != nil {
		stopContextWatcher()
		if closeErr := rawConn.Close(); closeErr != nil {
			logutil.Debugf("[RCON] connection close after dial error: %v", closeErr)
		}
		return nil, nil, err
	}

	return conn, stopContextWatcher, nil
}

func closeConnOnContextDone(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if err := conn.Close(); err != nil {
				logutil.Debugf("[RCON] connection close after context cancellation: %v", err)
			}
		case <-done:
		}
	}()

	return func() {
		close(done)
	}
}

func mapContextError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ErrCanceled
	}
	return err
}

func mapTimeoutError(err error) error {
	if err == nil {
		return nil
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrTimeout
	}

	return nil
}
