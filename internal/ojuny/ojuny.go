package ojuny

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Runner interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error)
}

type Options struct {
	Hostname     string
	Account      string
	DiskDevice   string
	FirmwareEFI  bool
	AdminKey     string
	StateVersion string
}

type Preparer struct {
	Runner         Runner
	Git            string
	PrepareCommand []string
}

func (p *Preparer) Clone(ctx context.Context, url, branch, dir string) error {
	git := p.git()
	output, err := p.Runner.Run(ctx, "", nil, git, "clone", "--branch", branch, "--single-branch", url, dir)
	if err != nil {
		return fmt.Errorf("ojuny: clone: %w: %s", err, output)
	}
	return nil
}

func (p *Preparer) BackupRef(ctx context.Context, dir, jobID string) (string, error) {
	git := p.git()
	head, err := p.Runner.Run(ctx, dir, nil, git, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("ojuny: read HEAD: %w: %s", err, head)
	}
	ref := fmt.Sprintf("refs/nixconnect/backup/%s-%d", jobID, time.Now().Unix())
	if output, err := p.Runner.Run(ctx, dir, nil, git, "update-ref", ref, strings.TrimSpace(head)); err != nil {
		return "", fmt.Errorf("ojuny: backup ref: %w: %s", err, output)
	}
	return ref, nil
}

func (p *Preparer) Prepare(ctx context.Context, dir string, options Options) ([]string, error) {
	if len(p.PrepareCommand) > 0 {
		args := append([]string(nil), p.PrepareCommand[1:]...)
		args = append(args, "--root", dir, "--hostname", options.Hostname, "--username", options.Account, "--disk", options.DiskDevice)
		if options.FirmwareEFI {
			args = append(args, "--firmware", "efi")
		} else {
			args = append(args, "--firmware", "bios")
		}
		if output, err := p.Runner.Run(ctx, dir, nil, p.PrepareCommand[0], args...); err != nil {
			return nil, fmt.Errorf("ojuny: prepare: %w: %s", err, output)
		}
		return nil, nil
	}
	return p.writeModules(dir, options)
}

func (p *Preparer) writeModules(dir string, options Options) ([]string, error) {
	modules := filepath.Join(dir, "modules")
	if err := os.MkdirAll(modules, 0o700); err != nil {
		return nil, err
	}
	hardwarePath := filepath.Join(modules, "00-hardware.nix")
	userPath := filepath.Join(modules, "00-user.nix")
	importsPath := filepath.Join(modules, "00-imports.nix")

	hardware := fmt.Sprintf(`{ self, inputs, ... }:
{
  flake.modules.nixos.system-hardware = { lib, ... }:
  {
    imports = [ inputs.disko.nixosModules.disko ];

    boot.initrd.availableKernelModules = [
      "ahci"
      "dm_mod"
      "hid_generic"
      "nvme"
      "sd_mod"
      "sr_mod"
      "usb_storage"
      "usbhid"
      "xhci_pci"
    ];

    disko.devices.disk.main = {
      device = %q;
      type = "disk";
      content = {
        type = "gpt";
        partitions = {
          boot = { size = "1M"; type = "EF02"; priority = 1; };
          ESP = {
            size = "1G";
            type = "EF00";
            priority = 2;
            content = {
              type = "filesystem";
              format = "vfat";
              mountpoint = "/boot";
              mountOptions = [ "umask=0077" ];
            };
          };
          root = {
            size = "100%%";
            priority = 4;
            content = {
              type = "filesystem";
              format = "ext4";
              mountpoint = "/";
            };
          };
        };
      };
    };

    boot.loader.systemd-boot.enable = lib.mkForce %t;
    boot.loader.efi.canTouchEfiVariables = lib.mkForce %t;
    boot.loader.grub.enable = lib.mkForce %t;
    boot.loader.grub.devices = lib.mkIf %t [ %q ];
    boot.loader.grub.efiSupport = lib.mkForce %t;
    boot.loader.grub.efiInstallAsRemovable = lib.mkForce %t;

    ojuny.network.deploy-rs.enable = lib.mkDefault true;
    nixpkgs.hostPlatform = lib.mkDefault "x86_64-linux";
  };
}
`, options.DiskDevice, options.FirmwareEFI, options.FirmwareEFI, !options.FirmwareEFI, !options.FirmwareEFI, options.DiskDevice, options.FirmwareEFI, options.FirmwareEFI)

	user := fmt.Sprintf(`{ self, inputs, ... }:
{
  flake.modules.nixos.users = { ... }:
  {
    users.users.%q = {
      isNormalUser = true;
      extraGroups = [ "wheel" "networkmanager" ];
      openssh.authorizedKeys.keys = [ %q ];
    };
  };
}
`, options.Account, options.AdminKey)

	imports := fmt.Sprintf(`{ self, ... }:
{
  flake.modules.nixos.host-imports = { ... }:
  {
    imports = [
      self.modules.nixos.system-hardware
      self.modules.nixos.users
    ];
  };
}
`)

	for path, content := range map[string]string{
		hardwarePath: hardware,
		userPath:     user,
		importsPath:  imports,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return nil, err
		}
	}
	return []string{hardwarePath, userPath, importsPath}, nil
}

func (p *Preparer) Commit(ctx context.Context, dir, message string) error {
	git := p.git()
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-m", message},
	} {
		if output, err := p.Runner.Run(ctx, dir, nil, git, args...); err != nil {
			return fmt.Errorf("ojuny: %s: %w: %s", strings.Join(args, " "), err, output)
		}
	}
	return nil
}

func (p *Preparer) Push(ctx context.Context, dir, branch string) error {
	git := p.git()
	if output, err := p.Runner.Run(ctx, dir, nil, git, "push", "origin", branch); err != nil {
		return fmt.Errorf("ojuny: push: %w: %s", err, output)
	}
	return nil
}

func (p *Preparer) Publish(ctx context.Context, dir, remoteURL, branch string) error {
	git := p.git()
	status, err := p.Runner.Run(ctx, dir, nil, git, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("ojuny: status: %w: %s", err, status)
	}
	if strings.TrimSpace(status) != "" {
		if err := p.Commit(ctx, dir, "Prepare the target configuration"); err != nil {
			return err
		}
	}
	refs, err := p.Runner.Run(ctx, dir, nil, git, "ls-remote", remoteURL)
	if err != nil {
		return fmt.Errorf("ojuny: inspect remote: %w: %s", err, refs)
	}
	if strings.TrimSpace(refs) != "" {
		return errors.New("ojuny: the new remote is not empty")
	}
	if output, err := p.Runner.Run(ctx, dir, nil, git, "remote", "add", "nixconnect", remoteURL); err != nil {
		return fmt.Errorf("ojuny: add remote: %w: %s", err, output)
	}
	if output, err := p.Runner.Run(ctx, dir, nil, git, "push", "--set-upstream", "nixconnect", branch); err != nil {
		return fmt.Errorf("ojuny: publish: %w: %s", err, output)
	}
	head, err := p.Runner.Run(ctx, dir, nil, git, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	remoteHead, err := p.Runner.Run(ctx, dir, nil, git, "ls-remote", remoteURL, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	if !strings.Contains(remoteHead, strings.TrimSpace(head)) {
		return errors.New("ojuny: remote head does not match the local commit")
	}
	return nil
}

func (p *Preparer) git() string {
	if p.Git == "" {
		return "git"
	}
	return p.Git
}
