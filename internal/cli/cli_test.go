package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type fakeService struct{}

func (fakeService) Status(context.Context) httpapi.Status {
	return httpapi.Status{ID: "fake-id", ShortID: "fake-id", Hostname: "fake"}
}

func (fakeService) Devices(context.Context) []httpapi.Device {
	return []httpapi.Device{{ID: "peer", ShortID: "peer-id", Hostname: "peer"}}
}

func (fakeService) Register(context.Context, string, httpapi.RegisterRequest) error { return nil }
func (fakeService) Unregister(context.Context, string, string) error                { return nil }

func (fakeService) Deploy(context.Context, string, httpapi.DeployRequest) (string, error) {
	return "deploy-job", nil
}

func (fakeService) Update(context.Context, string, httpapi.UpdateRequest) (string, error) {
	return "update-job", nil
}

func (fakeService) SSH(context.Context, string) (httpapi.SSHInfo, error) {
	return httpapi.SSHInfo{Host: "10.0.0.2", User: "harness", Port: 22}, nil
}

func (fakeService) Jobs(context.Context) []jobs.Job {
	return []jobs.Job{{ID: "job", State: jobs.StateSucceeded}}
}

func (fakeService) Job(_ context.Context, id string) (jobs.Job, error) {
	if id != "job" {
		return jobs.Job{}, errors.New("unknown job")
	}
	return jobs.Job{ID: "job", State: jobs.StateSucceeded}, nil
}

func (fakeService) Confirm(context.Context, string, string) error { return nil }
func (fakeService) Cancel(context.Context, string) error          { return nil }

func (fakeService) PairStart(context.Context, httpapi.PairRequest) (httpapi.PairResponse, error) {
	return httpapi.PairResponse{ID: "pair", Code: "123456"}, nil
}

func (fakeService) PairConfirm(context.Context, httpapi.PairConfirmRequest) error { return nil }
func (fakeService) PairPending(context.Context) []pairing.Request {
	return []pairing.Request{{ID: "pair", PeerID: "peer", Code: "123456"}}
}

func (fakeService) ShareKey(context.Context, httpapi.KeyShareRequest) (pairing.Envelope, error) {
	return pairing.Envelope{Version: 1, Ephemeral: "e", Nonce: "n", Ciphertext: "c"}, nil
}
func (fakeService) ImportSharedKey(context.Context, string) (string, error) {
	return "ssh-ed25519 AAAA", nil
}

func (fakeService) ResetRegistration(context.Context) error { return nil }
func (fakeService) TrustRecords(context.Context) []trust.Record {
	return []trust.Record{{Fingerprint: "peer"}}
}
func (fakeService) RevokeTrust(context.Context, string) error { return nil }
func (fakeService) RotateAdminKey(context.Context) (string, error) {
	return "ssh-ed25519 AAAA", nil
}

func (fakeService) BuildISO(context.Context, httpapi.ISORequest) (string, error) {
	return "iso-job", nil
}

func (fakeService) Publish(context.Context, httpapi.PublishRequest) (string, error) {
	return "publish-job", nil
}

func startServer(t *testing.T) (string, func()) {
	t.Helper()
	directory := t.TempDir()
	socket := filepath.Join(directory, "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	server := httpapi.NewServer(fakeService{}, nil)
	go func() {
		_ = server.ServeLocal(ctx, socket)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("control socket did not appear")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(cancel)
	return socket, cancel
}

func captureStdout(t *testing.T, function func() error) (string, error) {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	runErr := function()
	_ = writer.Close()
	os.Stdout = original
	data, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), runErr
}

func runCLI(t *testing.T, socket string, arguments ...string) (string, error) {
	t.Helper()
	all := append([]string{"--socket", socket}, arguments...)
	return captureStdout(t, func() error {
		return Run(all)
	})
}

func TestRunReadCommands(t *testing.T) {
	socket, _ := startServer(t)
	output, err := runCLI(t, socket, "status")
	if err != nil || !strings.Contains(output, "fake") {
		t.Fatalf("status: %q %v", output, err)
	}
	output, err = runCLI(t, socket, "devices")
	if err != nil || !strings.Contains(output, "peer") {
		t.Fatalf("devices: %q %v", output, err)
	}
	output, err = runCLI(t, socket, "info", "peer")
	if err != nil || !strings.Contains(output, "peer") {
		t.Fatalf("info: %q %v", output, err)
	}
	output, err = runCLI(t, socket, "info", "missing")
	if err == nil {
		t.Fatalf("unknown info was accepted: %q", output)
	}
	output, err = runCLI(t, socket, "jobs")
	if err != nil || !strings.Contains(output, "job") {
		t.Fatalf("jobs: %q %v", output, err)
	}
	output, err = runCLI(t, socket, "job", "job")
	if err != nil || !strings.Contains(output, "job") {
		t.Fatalf("job: %q %v", output, err)
	}
	output, err = runCLI(t, socket, "job", "missing")
	if err == nil {
		t.Fatalf("unknown job was accepted: %q", output)
	}
}

func TestRunMutationCommands(t *testing.T) {
	socket, _ := startServer(t)
	if output, err := runCLI(t, socket, "register", "peer", "--flake", "repo", "--branch", "main", "--host", "host"); err != nil {
		t.Fatalf("register: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "unregister", "peer"); err != nil {
		t.Fatalf("unregister: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "deploy", "peer", "--flake", "repo", "--branch", "main", "--host", "host"); err != nil || !strings.Contains(output, "deploy-job") {
		t.Fatalf("deploy: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "update", "peer"); err != nil || !strings.Contains(output, "update-job") {
		t.Fatalf("update: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "publish", "--remote", "https://example.invalid/new", "--branch", "main"); err != nil || !strings.Contains(output, "publish-job") {
		t.Fatalf("publish: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "build-iso"); err != nil || !strings.Contains(output, "iso-job") {
		t.Fatalf("build-iso: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "build-iso", "-o", "/tmp/nix-connect-test.iso"); err != nil || !strings.Contains(output, "/tmp/nix-connect-test.iso") {
		t.Fatalf("build-iso output: %q %v", output, err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if output, err := runCLI(t, socket, "build-iso", "-o", "~/nix-connect-test"); err != nil || !strings.Contains(output, filepath.Join(home, "nix-connect-test")) {
		t.Fatalf("build-iso home output: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "confirm", "job"); err != nil {
		t.Fatalf("confirm: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "cancel", "job"); err != nil {
		t.Fatalf("cancel: %q %v", output, err)
	}
}

func TestRunPairAndKey(t *testing.T) {
	socket, _ := startServer(t)
	output, err := runCLI(t, socket, "pair", "start", "peer", "key")
	if err != nil || !strings.Contains(output, "123456") {
		t.Fatalf("pair start: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "pair", "confirm", "pair", "123456"); err != nil {
		t.Fatalf("pair confirm: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "pair", "pending"); err != nil || !strings.Contains(output, "123456") {
		t.Fatalf("pair pending: %q %v", output, err)
	}
	output, err = runCLI(t, socket, "key", "share", "peer")
	if err != nil || !strings.Contains(output, "ssh-ed25519") {
		t.Fatalf("key share: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "key", "bogus"); err == nil {
		t.Fatalf("invalid key command was accepted: %q", output)
	}
}

func TestRunResetAndTrust(t *testing.T) {
	socket, _ := startServer(t)
	if output, err := runCLI(t, socket, "reset"); err != nil {
		t.Fatalf("reset: %q %v", output, err)
	}
	output, err := runCLI(t, socket, "trust", "list")
	if err != nil || !strings.Contains(output, "peer") {
		t.Fatalf("trust list: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "trust", "revoke", "peer"); err != nil {
		t.Fatalf("trust revoke: %q %v", output, err)
	}
	if output, err := runCLI(t, socket, "trust", "bogus"); err == nil {
		t.Fatalf("invalid trust command was accepted: %q", output)
	}
	if output, err := runCLI(t, socket, "key", "rotate"); err != nil || !strings.Contains(output, "ssh-ed25519") {
		t.Fatalf("key rotate: %q %v", output, err)
	}
}

func TestRunHelpAndUnknown(t *testing.T) {
	output, err := runCLI(t, filepath.Join(t.TempDir(), "missing.sock"), "help")
	if err != nil || !strings.Contains(output, "Commands:") {
		t.Fatalf("help: %q %v", output, err)
	}
	if err := Run([]string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if err := Run([]string{"-h"}); err != nil {
		t.Fatalf("-h: %v", err)
	}
	if _, err := runCLI(t, filepath.Join(t.TempDir(), "missing.sock"), "unknown"); err == nil {
		t.Fatal("unknown command was accepted")
	}
}

func TestRunMissingSocket(t *testing.T) {
	if err := Run([]string{"--socket", filepath.Join(t.TempDir(), "missing.sock"), "status"}); err == nil {
		t.Fatal("missing socket was accepted")
	}
}

func TestPasswordValue(t *testing.T) {
	if value, err := passwordValue("inline", false); err != nil || value != "inline" {
		t.Fatalf("unexpected inline value: %q %v", value, err)
	}
	original := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("secret\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	os.Stdin = reader
	value, err := passwordValue("", true)
	os.Stdin = original
	_ = reader.Close()
	if err != nil || value != "secret" {
		t.Fatalf("unexpected stdin value: %q %v", value, err)
	}
}

func TestPrintJSONError(t *testing.T) {
	if err := printJSON("value", errors.New("boom")); err == nil || err.Error() != "boom" {
		t.Fatalf("unexpected print error: %v", err)
	}
}
