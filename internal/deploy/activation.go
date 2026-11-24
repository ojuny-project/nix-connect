package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ActivationSpec struct {
	Command        string
	Args           []string
	Closure        string
	TempDir        string
	PreviousSystem string
	Timeout        time.Duration
	MagicRollback  bool
	AutoRollback   bool
	UnitName       string
}

type Activation struct {
	Runner     Runner
	SystemdRun string
}

func (a *Activation) Start(ctx context.Context, spec ActivationSpec) (string, error) {
	if err := os.MkdirAll(spec.TempDir, 0o700); err != nil {
		return "", err
	}
	canary := CanaryPath(spec.TempDir, spec.Closure)
	args := append([]string(nil), spec.Args...)
	if spec.MagicRollback {
		args = append(args,
			"--confirm-timeout", fmt.Sprintf("%d", int(spec.Timeout.Seconds())),
			"--magic-rollback",
		)
	}
	if spec.AutoRollback {
		args = append(args, "--auto-rollback")
	}

	systemdRun := a.SystemdRun
	if systemdRun != "" {
		unit := spec.UnitName
		if unit == "" {
			unit = "nixconnect-activate-" + filepath.Base(spec.TempDir)
		}
		command := append([]string{"--unit", unit, "--collect", "--", spec.Command}, args...)
		if output, err := a.Runner.Run(ctx, "", nil, systemdRun, command...); err != nil {
			return canary, fmt.Errorf("deploy: start activation: %w: %s", err, output)
		}
		return canary, nil
	}

	if output, err := a.Runner.Run(ctx, "", nil, spec.Command, args...); err != nil {
		return canary, fmt.Errorf("deploy: activation: %w: %s", err, output)
	}
	return canary, nil
}

func (a *Activation) Confirm(canary string) error {
	return ConfirmCanary(canary)
}
