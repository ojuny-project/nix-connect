package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls   []string
	outputs map[string]string
	errs    map[string]error
}

func (f *fakeRunner) Run(_ context.Context, _ string, _ []string, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err, ok := f.errs[key]; ok {
		return f.outputs[key], err
	}
	return f.outputs[key], nil
}

func TestInstallCommands(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		"nix build --no-link --print-out-paths --flake path:/work#nixosConfigurations.host.config.system.build.toplevel": "/nix/store/abc-system\n",
	}}
	install := &Install{Runner: runner}
	ctx := context.Background()
	if err := install.Clone(ctx, "https://example.invalid/flake", "main", "/work"); err != nil {
		t.Fatal(err)
	}
	closure, err := install.Build(ctx, "/work", "host")
	if err != nil {
		t.Fatal(err)
	}
	if closure != "/nix/store/abc-system" {
		t.Fatalf("unexpected closure %q", closure)
	}
	if err := install.Copy(ctx, "root@10.0.0.2", closure); err != nil {
		t.Fatal(err)
	}
	if err := install.Install(ctx, "/work", "host"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"git clone --branch main --single-branch https://example.invalid/flake /work",
		"nix copy --to ssh-ng://root@10.0.0.2 /nix/store/abc-system",
		"disko --mode disko path:/work#host",
		"nixos-install --flake path:/work#host --no-root-passwd",
	}
	for _, expected := range want {
		found := false
		for _, call := range runner.calls {
			if call == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing call %q in %v", expected, runner.calls)
		}
	}
}

func TestStageTreeCopiesNestedFiles(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "nix"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nix", "value.nix"), []byte("{ }\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := StageTree(source, filepath.Join(destination, "source")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "source", "nix", "value.nix"))
	if err != nil || string(data) != "{ }\n" {
		t.Fatalf("unexpected staged file: %q %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(destination, "source", "prepaired.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("staged tree is not writable: %v", err)
	}
}

func TestStageStateCopiesTheDurableFiles(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	files := map[string]string{
		"identity/identity.key": "private",
		"identity/identity.pub": "public",
		"registration.json":     "{}",
		"trust.json":            "{}",
		"config.json":           "{}",
		"jobs/job.json":         "{}",
	}
	for relative, content := range files {
		path := filepath.Join(source, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := StageState(source, destination, "job"); err != nil {
		t.Fatal(err)
	}
	for relative, content := range files {
		path := filepath.Join(destination, "var", "lib", "nix-connect", relative)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing %s: %v", relative, err)
		}
		if string(data) != content {
			t.Fatalf("unexpected content for %s: %q", relative, data)
		}
	}
}

func TestActivationStartsAndConfirmsMagicRollback(t *testing.T) {
	runner := &fakeRunner{}
	activation := &Activation{Runner: runner}
	temporary := t.TempDir()
	canary, err := activation.Start(context.Background(), ActivationSpec{
		Command:       "activate-rs",
		Args:          []string{"activate", "/nix/store/abc-system"},
		Closure:       "/nix/store/abc-system",
		TempDir:       temporary,
		Timeout:       30 * time.Second,
		MagicRollback: true,
		AutoRollback:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanary(canary); err == nil {
		t.Fatal("activation armed the canary before deploy-rs created it")
	}
	if err := ArmCanary(canary); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanary(canary); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("unexpected calls %v", runner.calls)
	}
	call := runner.calls[0]
	for _, expected := range []string{"activate-rs", "activate", "--confirm-timeout 30", "--magic-rollback", "--auto-rollback"} {
		if !strings.Contains(call, expected) {
			t.Fatalf("call %q is missing %q", call, expected)
		}
	}
	if err := activation.Confirm(canary); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanary(canary); err == nil {
		t.Fatal("canary was not removed")
	}
}

func TestActivationUsesSystemdRun(t *testing.T) {
	runner := &fakeRunner{}
	activation := &Activation{Runner: runner, SystemdRun: "systemd-run"}
	canary, err := activation.Start(context.Background(), ActivationSpec{
		Command:       "activate-rs",
		Closure:       "/nix/store/abc-system",
		TempDir:       t.TempDir(),
		Timeout:       time.Minute,
		MagicRollback: true,
		UnitName:      "nixconnect-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ConfirmCanary(canary)
	if len(runner.calls) != 1 {
		t.Fatalf("unexpected calls %v", runner.calls)
	}
	if !strings.HasPrefix(runner.calls[0], "systemd-run --unit nixconnect-test --collect -- activate-rs") {
		t.Fatalf("unexpected systemd-run call %q", runner.calls[0])
	}
}

func TestActivationWithoutMagicRollback(t *testing.T) {
	runner := &fakeRunner{}
	activation := &Activation{Runner: runner}
	if _, err := activation.Start(context.Background(), ActivationSpec{
		Command: "activate-rs",
		Closure: "/nix/store/abc-system",
		TempDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(runner.calls[0], "--magic-rollback") {
		t.Fatalf("magic rollback was enabled: %q", runner.calls[0])
	}
}

func TestUpdateWrapperHasNoDisko(t *testing.T) {
	spec := WrapperSpec{
		TargetURL: "https://example.invalid/flake",
		Host:      "workstation",
		System:    "x86_64-linux",
		Account:   "alice",
	}
	wrapper := UpdateWrapper(spec)
	if !strings.Contains(wrapper, "workstation") || !strings.Contains(wrapper, "alice") {
		t.Fatalf("wrapper is missing target fields:\n%s", wrapper)
	}
	if strings.Contains(wrapper, "disko.nixosModules.disko") {
		t.Fatal("update wrapper pulled in disko")
	}
	directory := t.TempDir()
	if err := WriteWrapper(directory, spec, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "disko.nix")); !os.IsNotExist(err) {
		t.Fatal("update wrapper wrote a disko module")
	}
	if _, err := os.Stat(filepath.Join(directory, "admin-key.nix")); err != nil {
		t.Fatal(err)
	}
}
