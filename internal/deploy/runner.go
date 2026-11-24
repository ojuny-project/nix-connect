package deploy

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	if len(env) > 0 {
		command.Env = append(os.Environ(), env...)
	}
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
