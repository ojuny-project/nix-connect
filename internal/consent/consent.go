package consent

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

var accountPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`)

type Result struct {
	Output   string
	ExitCode int
}

type Runner interface {
	Run(ctx context.Context, name string, args []string, stdin io.Reader) (Result, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader) (Result, error) {
	command := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		command.Stdin = stdin
	}
	output, err := command.CombinedOutput()
	if err == nil {
		return Result{Output: string(output), ExitCode: 0}, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return Result{Output: string(output), ExitCode: exitError.ExitCode()}, nil
	}
	return Result{Output: string(output), ExitCode: -1}, err
}

type Gate struct {
	Service string
	Runner  Runner
}

func New(service string) *Gate {
	return &Gate{Service: service, Runner: ExecRunner{}}
}

func (g *Gate) Authorize(ctx context.Context, account, password string) (bool, error) {
	if !accountPattern.MatchString(account) {
		return false, nil
	}
	if g.passwordlessSudo(ctx, account) {
		return true, nil
	}
	if password == "" {
		return false, nil
	}
	if !g.inWheel(ctx, account) {
		return false, nil
	}
	return g.pamAuthenticate(ctx, account, password)
}

func (g *Gate) passwordlessSudo(ctx context.Context, account string) bool {
	result, err := g.run(ctx, "runuser", []string{"-u", account, "--", "sudo", "-n", "true"}, nil)
	return err == nil && result.ExitCode == 0
}

func (g *Gate) inWheel(ctx context.Context, account string) bool {
	result, err := g.run(ctx, "id", []string{"-nG", account}, nil)
	if err != nil || result.ExitCode != 0 {
		return false
	}
	for _, group := range strings.Fields(result.Output) {
		if group == "wheel" {
			return true
		}
	}
	return false
}

func (g *Gate) pamAuthenticate(ctx context.Context, account, password string) (bool, error) {
	service := g.Service
	if service == "" {
		service = "nixconnect"
	}
	result, err := g.run(ctx, "pamtester", []string{service, account, "authenticate"}, strings.NewReader(password+"\n"))
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

func (g *Gate) run(ctx context.Context, name string, args []string, stdin io.Reader) (Result, error) {
	runner := g.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return runner.Run(ctx, name, args, stdin)
}
