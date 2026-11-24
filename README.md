# nix-connect

Auto-discovering LAN management utility for NixOS devices.

nix-connect discovers devices on a local network, pairs administration devices,
and exposes SSH, device information, flake installation, deploy-rs updates and
flake registration. It ships a daemon, a CLI with a TUI (`nixconnect`, alias
`ncon`) and a GTK4 client.

Status: the daemon, CLI, TUI, GTK4 client, deploy and update flows, pairing,
key rotation and sharing, trust revocation, publish and the deploytarget ISO are
implemented. The local control socket does not yet identify callers by UID and
the daemon runs as root; the design's service-user split is open. The governing
design is [docs/design.md](docs/design.md). The delivery plan is
[docs/implementation-plan.md](docs/implementation-plan.md).

## Components

- `nixconnect-daemon`: discovery, identity, pairing, registration and jobs.
- `nixconnect`: the CLI. Running it with no arguments opens the TUI.
- `nixconnect-gui`: the GTK4 client.

The frontends are clients of the daemon and require it at runtime.

## Binaries

Each release publishes three self-extracting bundles and a small flake that
installs them without a source build:

- `nixconnect.bin`
- `nixconnect-daemon.bin`
- `nixconnect-gui.bin`

The bundles are published under
`https://github.com/ojuny-project/nix-connect/releases/download/<tag>/`. They
carry their full closure and run on a plain Linux system. `SHA256SUMS` is
published next to them.

The release flake is `nix-connect-flake.tar.gz` in the same release. Add it as
an input when the runtime should install the pinned asset instead of compiling:

```nix
{
  inputs.nix-connect-bin.url =
    "tarball+https://github.com/ojuny-project/nix-connect/releases/download/v0.1.0/nix-connect-flake.tar.gz";
}
```

Use the packages in a system configuration:

```nix
{ inputs, pkgs, ... }:
{
  environment.systemPackages = [
    inputs.nix-connect-bin.packages.${pkgs.system}.nixconnect
    inputs.nix-connect-bin.packages.${pkgs.system}.nixconnect-daemon
    inputs.nix-connect-bin.packages.${pkgs.system}.nixconnect-gui
  ];
}
```

The release flake installs the exact uploaded asset and pins its hash. It does
not replace the source flake for the NixOS module, the deploytarget ISO or the
development build.

Run the CLI without installing:

```console
$ nix run github:ojuny-project/nix-connect#nixconnect
```

## Development

```console
$ nix develop
$ go test ./...
$ go build ./...
$ go vet ./...
```

The devshell contains Go, gcc, pkg-config, GTK4, libadwaita, git, openssh,
pamtester, deploy-rs, disko and nixos-anywhere. The devshell toolchain is Go
1.27.3 on x86_64-linux.

Verification:

```console
$ nix develop --command go test ./...
$ nix develop --command go vet ./...
$ nix flake check
$ nix build .#checks.x86_64-linux.single-node
$ nix build .#checks.x86_64-linux.two-node
$ nix build .#nixosConfigurations.deploytarget.config.system.build.isoImage
```

Run the daemon against a temporary state directory:

```console
$ nix develop --command go run ./cmd/nixconnect-daemon \
    --state-dir /tmp/nix-connect-state \
    --socket /tmp/nix-connect.sock \
    --agent-socket /tmp/nix-connect-agent.sock
```

Run the CLI against that socket:

```console
$ nix develop --command go run ./cmd/nixconnect --socket /tmp/nix-connect.sock
```

## NixOS

```nix
{
  inputs.nix-connect.url = "github:ojuny-project/nix-connect";

  outputs = inputs@{ nixpkgs, ... }: {
    nixosConfigurations.host = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        inputs.nix-connect.nixosModules.default
        {
          services.nix-connect = {
            enable = true;
            discovery.auto = true;
          };
        }
      ];
    };
  };
}
```

The module creates the service, the state and runtime directories, the
`nix-connect` group, the local PAM service and the firewall entries.

## Deploytarget ISO

```console
$ nix build .#nixosConfigurations.deploytarget.config.system.build.isoImage
```

The daemon builds the same image as a job:

```console
$ nixconnect build-iso
```

The runtime form embeds the administration identity, the administration SSH
public key and the pre-pairing trust record. It copies the installed
`nixconnect-source`, writes the current device material and builds the ISO as a
Nix out-link. The default out-link is `deploy-target-iso` in the current
directory; `--output PATH` or `-o PATH` overrides it. The out-link points to the
Nix result directory, so the image is at
`deploy-target-iso/iso/deploytarget-<short-id>.iso`. The daemon does not copy
the image. The direct expression builds the generic image used by CI.

The image boots to a console, connects wired networking with DHCP and starts the
daemon. On Wi-Fi, run `nmtui` from the console. Module options for
`prepairedFingerprint`, `prepairedPublicKey` and `adminAuthorizedKey` embed the
administration device at build time.

## Documentation

- [Design](docs/design.md)
- [Implementation plan](docs/implementation-plan.md)

## License

GPL-3.0-only. See [LICENSE](LICENSE).
