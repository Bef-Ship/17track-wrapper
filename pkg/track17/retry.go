package track17

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	sdk "github.com/SolaTyolo/17track-sdk"
)

// ErrRetryable marks a transient 17TRACK failure that should be re-queued / retried.
var ErrRetryable = errors.New("17track: retryable")

var httpStatusRe = regexp.MustCompile(`(?i)http status (\d{3})`)

// IsRetryable reports whether err should be retried (queue / asynq) rather than
// returned as a permanent failure to the caller.
//
// Retry: rate limits (429), upstream 5xx, network / timeout / limiter wait cancel.
// Do not retry: auth / validation / business APIError codes / rejected items.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRetryable) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// Canceled by client request → not our retry; DeadlineExceeded from HTTP → retry.
		return errors.Is(err, context.DeadlineExceeded)
	}

	var ae *sdk.APIError
	if errors.As(err, &ae) {
		// Top-level business codes are permanent (invalid token shape, quota exhausted
		// messages still surface as APIError — treat as non-retry unless message hints
		// throttling).
		msg := strings.ToLower(ae.Message)
		if strings.Contains(msg, "rate") || strings.Contains(msg, "too many") ||
			strings.Contains(msg, "limit") || strings.Contains(msg, "throttle") {
			return true
		}
		return false
	}

	var rejected *sdk.RejectedItemsError
	if errors.As(err, &rejected) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	msg := err.Error()
	if m := httpStatusRe.FindStringSubmatch(msg); len(m) == 2 {
		code, _ := strconv.Atoi(m[1])
		if code == 429 || code == 502 || code == 503 || code == 504 {
			return true
		}
		return false
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "http do:") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "temporary") {
		return true
	}
	return false
}

// WrapRetryable annotates err as retryable when IsRetryable(err).
func WrapRetryable(err error) error {
	if err == nil || errors.Is(err, ErrRetryable) {
		return err
	}
	if IsRetryable(err) {
		return errors.Join(ErrRetryable, err)
	}
	return err
}
