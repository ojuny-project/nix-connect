package consent

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type fakeRunner struct {
	results map[string]Result
}

func (f fakeRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader) (Result, error) {
	key := name + " " + strings.Join(args, " ")
	if result, ok := f.results[key]; ok {
		return result, nil
	}
	return Result{ExitCode: 1}, nil
}

func TestPasswordlessSudo(t *testing.T) {
	gate := &Gate{Service: "nixconnect", Runner: fakeRunner{results: map[string]Result{
		"runuser -u nixos -- sudo -n true": {ExitCode: 0},
	}}}
	ok, err := gate.Authorize(context.Background(), "nixos", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected passwordless authorization")
	}
}

func TestRequiresWheelAndPAM(t *testing.T) {
	gate := &Gate{Service: "nixconnect", Runner: fakeRunner{results: map[string]Result{
		"runuser -u alice -- sudo -n true":        {ExitCode: 1},
		"id -nG alice":                            {ExitCode: 0, Output: "alice wheel\n"},
		"pamtester nixconnect alice authenticate": {ExitCode: 0},
	}}}
	ok, err := gate.Authorize(context.Background(), "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected PAM authorization")
	}
}

func TestRejectsNonWheel(t *testing.T) {
	gate := &Gate{Service: "nixconnect", Runner: fakeRunner{results: map[string]Result{
		"runuser -u bob -- sudo -n true": {ExitCode: 1},
		"id -nG bob":                     {ExitCode: 0, Output: "bob users\n"},
	}}}
	ok, err := gate.Authorize(context.Background(), "bob", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("non-wheel account was authorized")
	}
}

func TestInvalidAccountName(t *testing.T) {
	gate := &Gate{Service: "nixconnect", Runner: fakeRunner{}}
	ok, err := gate.Authorize(context.Background(), "bad name", "secret")
	if err != nil || ok {
		t.Fatalf("invalid account was accepted: %v %v", ok, err)
	}
}

func TestEmptyPasswordWithoutNOPASSWD(t *testing.T) {
	gate := &Gate{Service: "nixconnect", Runner: fakeRunner{results: map[string]Result{
		"runuser -u alice -- sudo -n true": {ExitCode: 1},
	}}}
	ok, err := gate.Authorize(context.Background(), "alice", "")
	if err != nil || ok {
		t.Fatalf("empty password was accepted: %v %v", ok, err)
	}
}

func TestPAMFailure(t *testing.T) {
	gate := &Gate{Service: "nixconnect", Runner: fakeRunner{results: map[string]Result{
		"runuser -u alice -- sudo -n true":        {ExitCode: 1},
		"id -nG alice":                            {ExitCode: 0, Output: "alice wheel\n"},
		"pamtester nixconnect alice authenticate": {ExitCode: 1},
	}}}
	ok, err := gate.Authorize(context.Background(), "alice", "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("failed PAM authentication was accepted")
	}
}

type failingRunner struct{}

func (failingRunner) Run(_ context.Context, name string, args []string, stdin io.Reader) (Result, error) {
	if name == "id" {
		return Result{ExitCode: 0, Output: "alice wheel\n"}, nil
	}
	if name == "pamtester" {
		return Result{}, errors.New("pam failed")
	}
	return Result{ExitCode: 1}, nil
}

func TestRunnerErrorPropagates(t *testing.T) {
	gate := &Gate{Service: "nixconnect", Runner: failingRunner{}}
	if _, err := gate.Authorize(context.Background(), "alice", "secret"); err == nil || err.Error() != "pam failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}
