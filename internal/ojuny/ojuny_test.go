package ojuny

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls  []string
	output map[string]string
}

func (f *fakeRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	return f.output[key], nil
}

func TestWriteModules(t *testing.T) {
	dir := t.TempDir()
	preparer := &Preparer{Runner: &fakeRunner{}}
	files, err := preparer.Prepare(context.Background(), dir, Options{
		Hostname:    "host",
		Account:     "alice",
		DiskDevice:  "/dev/disk/by-id/disk",
		FirmwareEFI: true,
		AdminKey:    "ssh-ed25519 AAAA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("unexpected files %v", files)
	}
	hardware, err := os.ReadFile(filepath.Join(dir, "modules", "00-hardware.nix"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hardware), "/dev/disk/by-id/disk") {
		t.Fatal("hardware module is missing the disk")
	}
	user, err := os.ReadFile(filepath.Join(dir, "modules", "00-user.nix"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(user), "ssh-ed25519 AAAA") {
		t.Fatal("user module is missing the admin key")
	}
}

func TestCloneCommitPush(t *testing.T) {
	runner := &fakeRunner{output: map[string]string{
		"git rev-parse HEAD": "abc123\n",
	}}
	preparer := &Preparer{Runner: runner}
	if err := preparer.Clone(context.Background(), "https://example.invalid/flake", "main", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := preparer.Commit(context.Background(), t.TempDir(), "prepare"); err != nil {
		t.Fatal(err)
	}
	if err := preparer.Push(context.Background(), t.TempDir(), "main"); err != nil {
		t.Fatal(err)
	}
	ref, err := preparer.BackupRef(context.Background(), t.TempDir(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "refs/nixconnect/backup/job-") {
		t.Fatalf("unexpected backup ref %q", ref)
	}
}

func TestPublishRejectsNonEmptyRemote(t *testing.T) {
	runner := &fakeRunner{output: map[string]string{
		"git status --porcelain":                    "",
		"git ls-remote https://example.invalid/new": "abc refs/heads/main\n",
	}}
	preparer := &Preparer{Runner: runner}
	err := preparer.Publish(context.Background(), t.TempDir(), "https://example.invalid/new", "main")
	if err == nil {
		t.Fatal("non-empty remote was accepted")
	}
}

func TestPublishPushes(t *testing.T) {
	runner := &fakeRunner{output: map[string]string{
		"git status --porcelain":                                    "",
		"git ls-remote https://example.invalid/new":                 "",
		"git remote add nixconnect https://example.invalid/new":     "",
		"git push --set-upstream nixconnect main":                   "",
		"git rev-parse HEAD":                                        "abc123\n",
		"git ls-remote https://example.invalid/new refs/heads/main": "abc123 refs/heads/main\n",
	}}
	preparer := &Preparer{Runner: runner}
	if err := preparer.Publish(context.Background(), t.TempDir(), "https://example.invalid/new", "main"); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareCommand(t *testing.T) {
	runner := &fakeRunner{}
	preparer := &Preparer{Runner: runner, PrepareCommand: []string{"prepare-repo"}}
	if _, err := preparer.Prepare(context.Background(), t.TempDir(), Options{
		Hostname:    "host",
		Account:     "alice",
		DiskDevice:  "/dev/disk/by-id/disk",
		FirmwareEFI: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("unexpected calls %v", runner.calls)
	}
	call := runner.calls[0]
	for _, expected := range []string{"prepare-repo", "--root", "--hostname host", "--username alice", "--disk /dev/disk/by-id/disk", "--firmware efi"} {
		if !strings.Contains(call, expected) {
			t.Fatalf("call %q is missing %q", call, expected)
		}
	}
}

func TestPublishCommitsDirtyWorktree(t *testing.T) {
	runner := &fakeRunner{output: map[string]string{
		"git status --porcelain":                                    " M modules/00-hardware.nix\n",
		"git ls-remote https://example.invalid/new":                 "",
		"git remote add nixconnect https://example.invalid/new":     "",
		"git push --set-upstream nixconnect main":                   "",
		"git rev-parse HEAD":                                        "abc123\n",
		"git ls-remote https://example.invalid/new refs/heads/main": "abc123 refs/heads/main\n",
	}}
	preparer := &Preparer{Runner: runner}
	if err := preparer.Publish(context.Background(), t.TempDir(), "https://example.invalid/new", "main"); err != nil {
		t.Fatal(err)
	}
	foundAdd, foundCommit := false, false
	for _, call := range runner.calls {
		if call == "git add -A" {
			foundAdd = true
		}
		if strings.HasPrefix(call, "git commit -m ") {
			foundCommit = true
		}
	}
	if !foundAdd || !foundCommit {
		t.Fatalf("dirty worktree was not committed: %v", runner.calls)
	}
}

func TestPublishRejectsHeadMismatch(t *testing.T) {
	runner := &fakeRunner{output: map[string]string{
		"git status --porcelain":                                    "",
		"git ls-remote https://example.invalid/new":                 "",
		"git remote add nixconnect https://example.invalid/new":     "",
		"git push --set-upstream nixconnect main":                   "",
		"git rev-parse HEAD":                                        "abc123\n",
		"git ls-remote https://example.invalid/new refs/heads/main": "def456 refs/heads/main\n",
	}}
	preparer := &Preparer{Runner: runner}
	if err := preparer.Publish(context.Background(), t.TempDir(), "https://example.invalid/new", "main"); err == nil {
		t.Fatal("mismatched remote head was accepted")
	}
}

func TestCommitStagesAndCommits(t *testing.T) {
	runner := &fakeRunner{}
	preparer := &Preparer{Runner: runner}
	if err := preparer.Commit(context.Background(), t.TempDir(), "prepare"); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 || runner.calls[0] != "git add -A" || runner.calls[1] != "git commit -m prepare" {
		t.Fatalf("unexpected calls %v", runner.calls)
	}
}
