package amqp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/terapps/gonveyor"
)

// RetryConfig controls the exponential backoff behavior on broker reconnection.
type RetryConfig struct {
	InitialDelay time.Duration
	Factor       float64
	MaxRetries   int
}

var retryBackoff = func(cfg RetryConfig) func() func(ctx context.Context) error {
	return func() func(ctx context.Context) error {
		delay := cfg.InitialDelay
		attempts := 0

		return func(ctx context.Context) error {
			if attempts >= cfg.MaxRetries {
				return errors.New("broker unreachable: max retries exceeded")
			}

			gonveyor.Logger.Info("retrying...", "in", delay, "attempt", fmt.Sprintf("%d/%d", attempts+1, cfg.MaxRetries))

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}

			attempts++
			delay = time.Duration(float64(delay) * cfg.Factor)

			return nil
		}
	}
}
