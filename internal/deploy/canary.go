package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrConfirmationTimeout = errors.New("deploy: confirmation timeout")
	ErrCanaryTimeout       = errors.New("deploy: canary was not created")
)

func WaitForCanary(ctx context.Context, path string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrCanaryTimeout
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return nil
			}
		}
	}
}

func CanaryPath(tempDir, closure string) string {
	hash := strings.TrimPrefix(closure, "/nix/store/")
	if index := strings.Index(hash, "-"); index >= 0 {
		hash = hash[:index]
	}
	if hash == "" {
		hash = "unknown"
	}
	return filepath.Join(tempDir, "deploy-rs-canary-"+hash)
}

func ArmCanary(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte{}, 0o600)
}

func ConfirmCanary(path string) error {
	if path == "" {
		return errors.New("deploy: empty canary path")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func WaitForConfirmation(ctx context.Context, path string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrConfirmationTimeout
		case <-ticker.C:
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				return nil
			}
		}
	}
}

func ConfirmationDeadline(timeout time.Duration) time.Time {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return time.Now().UTC().Add(timeout)
}

func ValidateCanary(path string) error {
	if path == "" {
		return errors.New("deploy: no canary path")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("deploy: canary unavailable: %w", err)
	}
	return nil
}
