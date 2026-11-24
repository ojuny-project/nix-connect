package deploy

import (
	"fmt"
	"os"
	"path/filepath"
)

type WrapperSpec struct {
	TargetURL    string
	Host         string
	System       string
	Account      string
	AdminKey     string
	DiskDevice   string
	FirmwareEFI  bool
	IncludeDisko bool
}

func InstallWrapper(spec WrapperSpec) string {
	diskoInputs := ""
	diskoModules := ""
	if spec.IncludeDisko {
		diskoInputs = "\n    disko.url = \"github:nix-community/disko\";"
		diskoModules = "\n          disko.nixosModules.disko\n          ./disko.nix"
	}
	return fmt.Sprintf(`{
  inputs = {
    target.url = %q;%s
    deploy-rs.url = "github:serokell/deploy-rs";
  };

  outputs = { self, target, deploy-rs%s }:
    let
      configuration = target.nixosConfigurations.%q.extendModules {
        modules = [%s
          ./admin-key.nix
        ];
      };
    in {
      nixosConfigurations.%q = configuration;
      deploy.nodes.%q = {
        hostname = "127.0.0.1";
        sshUser = %q;
        user = "root";
        profiles.system = {
          user = "root";
          path = deploy-rs.lib.%q.activate.nixos configuration;
        };
      };
    };
}
`, spec.TargetURL, diskoInputs, diskoInputArgument(spec.IncludeDisko), spec.Host, diskoModules, spec.Host, spec.Host, spec.Account, spec.System)
}

func UpdateWrapper(spec WrapperSpec) string {
	return fmt.Sprintf(`{
  inputs = {
    target.url = %q;
    deploy-rs.url = "github:serokell/deploy-rs";
  };

  outputs = { self, target, deploy-rs }:
    let
      configuration = target.nixosConfigurations.%q;
    in {
      nixosConfigurations.%q = configuration;
      deploy.nodes.%q = {
        hostname = "127.0.0.1";
        sshUser = %q;
        user = "root";
        profiles.system = {
          user = "root";
          path = deploy-rs.lib.%q.activate.nixos configuration;
        };
      };
    };
}
`, spec.TargetURL, spec.Host, spec.Host, spec.Host, spec.Account, spec.System)
}

func diskoInputArgument(included bool) string {
	if included {
		return ", disko"
	}
	return ""
}

func DiskoModule(spec WrapperSpec) string {
	return fmt.Sprintf(`{ lib, ... }:
{
  disko.devices.disk.main = {
    device = %q;
    type = "disk";
    content = {
      type = "gpt";
      partitions = {
        bios = {
          size = "1M";
          type = "EF02";
          priority = 1;
        };
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
}
`, spec.DiskDevice, spec.FirmwareEFI, spec.FirmwareEFI, !spec.FirmwareEFI, !spec.FirmwareEFI, spec.DiskDevice, spec.FirmwareEFI, spec.FirmwareEFI)
}

func AdminKeyModule(spec WrapperSpec) string {
	return fmt.Sprintf(`{ ... }:
{
  users.users.%q = {
    isNormalUser = true;
    extraGroups = [ "wheel" ];
    openssh.authorizedKeys.keys = [ %q ];
  };
}
`, spec.Account, spec.AdminKey)
}

func WriteWrapper(dir string, spec WrapperSpec, install bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	flake := UpdateWrapper(spec)
	if install {
		flake = InstallWrapper(spec)
	}
	files := map[string]string{
		"flake.nix":     flake,
		"admin-key.nix": AdminKeyModule(spec),
	}
	if install && spec.IncludeDisko {
		files["disko.nix"] = DiskoModule(spec)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}
