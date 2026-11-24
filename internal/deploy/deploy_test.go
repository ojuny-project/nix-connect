package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanaryLifecycle(t *testing.T) {
	temp := t.TempDir()
	path := CanaryPath(temp, "/nix/store/abc123-system")
	if filepath.Base(path) != "deploy-rs-canary-abc123" {
		t.Fatalf("unexpected canary path %s", path)
	}
	if err := ArmCanary(path); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanary(path); err != nil {
		t.Fatal(err)
	}
	if err := ConfirmCanary(path); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCanary(path); err == nil {
		t.Fatal("canary still exists")
	}
}

func TestWaitForConfirmation(t *testing.T) {
	temp := t.TempDir()
	path := CanaryPath(temp, "/nix/store/abc-system")
	if err := ArmCanary(path); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = ConfirmCanary(path)
	}()
	if err := WaitForConfirmation(context.Background(), path, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForConfirmationTimesOut(t *testing.T) {
	temp := t.TempDir()
	path := CanaryPath(temp, "/nix/store/abc-system")
	if err := ArmCanary(path); err != nil {
		t.Fatal(err)
	}
	if err := WaitForConfirmation(context.Background(), path, 30*time.Millisecond); err != ErrConfirmationTimeout {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestWaitForCanary(t *testing.T) {
	temp := t.TempDir()
	path := CanaryPath(temp, "/nix/store/abc-system")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = ArmCanary(path)
	}()
	if err := WaitForCanary(context.Background(), path, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForCanaryTimesOut(t *testing.T) {
	temp := t.TempDir()
	path := CanaryPath(temp, "/nix/store/abc-system")
	if err := WaitForCanary(context.Background(), path, 30*time.Millisecond); err != ErrCanaryTimeout {
		t.Fatalf("expected canary timeout, got %v", err)
	}
}

func TestWrapperContainsTargetSettings(t *testing.T) {
	spec := WrapperSpec{
		TargetURL:    "https://example.invalid/flake",
		Host:         "workstation",
		System:       "x86_64-linux",
		Account:      "alice",
		AdminKey:     "ssh-ed25519 AAAA",
		DiskDevice:   "/dev/disk/by-id/disk",
		FirmwareEFI:  true,
		IncludeDisko: true,
	}
	wrapper := InstallWrapper(spec)
	for _, expected := range []string{"workstation", "alice", "x86_64-linux", "disko.nixosModules.disko"} {
		if !strings.Contains(wrapper, expected) {
			t.Fatalf("wrapper is missing %q:\n%s", expected, wrapper)
		}
	}
	disko := DiskoModule(spec)
	if !strings.Contains(disko, "/dev/disk/by-id/disk") || !strings.Contains(disko, "systemd-boot") {
		t.Fatalf("unexpected disko module:\n%s", disko)
	}
}

func TestWriteWrapper(t *testing.T) {
	dir := t.TempDir()
	spec := WrapperSpec{
		TargetURL:    "https://example.invalid/flake",
		Host:         "host",
		System:       "x86_64-linux",
		Account:      "alice",
		AdminKey:     "ssh-ed25519 AAAA",
		DiskDevice:   "/dev/sda",
		FirmwareEFI:  true,
		IncludeDisko: true,
	}
	if err := WriteWrapper(dir, spec, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"flake.nix", "disko.nix", "admin-key.nix"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRollbackScript(t *testing.T) {
	script := RollbackScript("/nix/store/previous-system", "activate --magic-rollback")
	if !strings.Contains(script, "/nix/store/previous-system") {
		t.Fatal("rollback script lost the previous system")
	}
	if !strings.Contains(script, "systemctl reboot") {
		t.Fatal("rollback script does not reboot")
	}
}
