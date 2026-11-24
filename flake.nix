{
  description = "nix-connect";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems =
        function:
        nixpkgs.lib.genAttrs systems (
          system:
          function (import nixpkgs {
            inherit system;
          })
        );

      binaryInfo =
        if builtins.pathExists ./binary-url.json then
          builtins.fromJSON (builtins.readFile ./binary-url.json)
        else
          null;

      mkBinary =
        {
          system,
          tag ? (
            if binaryInfo == null || binaryInfo.latest == "" then
              throw "nix-connect: binary-url.json has no release"
            else
              binaryInfo.latest
          ),
          package,
        }:
        let
          release =
            if binaryInfo == null then
              throw "nix-connect: binary-url.json is missing"
            else
              binaryInfo.releases.${tag}
                or (throw "nix-connect: release '${tag}' is not recorded in binary-url.json");
          packageInfo =
            release.packages.${package}
              or (throw "nix-connect: package '${package}' is not recorded in release '${tag}'");
          pkgs = import nixpkgs { inherit system; };
        in
        pkgs.callPackage ./nix/binary.nix {
          name = package;
          program = packageInfo.program or package;
          inherit (packageInfo) file;
          info = {
            inherit (binaryInfo) repo system;
            inherit (release) tag version;
            inherit (packageInfo) url hash;
          };
        };

      packagesFor =
        pkgs:
        let
          base = import ./nix/packages.nix { inherit pkgs; };
          system = pkgs.stdenv.hostPlatform.system;
          binary =
            if binaryInfo != null && binaryInfo.latest != "" && binaryInfo.system == system then
              {
                nixconnect-binary = mkBinary {
                  inherit system;
                  package = "nixconnect";
                };
                nixconnect-daemon-binary = mkBinary {
                  inherit system;
                  package = "nixconnect-daemon";
                };
                nixconnect-gui-binary = mkBinary {
                  inherit system;
                  package = "nixconnect-gui";
                };
              }
            else
              { };
        in
        base
        // binary
        // {
          default = base.nixconnect-daemon;
        };
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gcc
            gnumake
            pkg-config
            gobject-introspection
            gtk4
            libadwaita
            git
            openssh
            rsync
            pamtester
            nixos-anywhere
            deploy-rs
            disko
            gopls
            go-tools
            jq
            curl
          ];
          shellHook = ''
            export GOTOOLCHAIN=local
          '';
        };
      });

      packages = forAllSystems packagesFor;

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.system}.nixconnect}/bin/nixconnect";
        };
        ci = {
          type = "app";
          program = "${
            pkgs.writeShellApplication {
              name = "ci";
              runtimeInputs = [ pkgs.nix ];
              text = ''
                exec nix flake check "$@"
              '';
            }
          }/bin/ci";
        };
      });

      checks = forAllSystems (
        pkgs:
        let
          packages = import ./nix/packages.nix { inherit pkgs; };
        in
        {
          nixconnect-daemon = packages.nixconnect-daemon;
          nixconnect = packages.nixconnect;
          nixconnect-gui = packages.nixconnect-gui;
          unit-tests = packages.nixconnect-tests;
          single-node = import ./nix/tests/single-node.nix { inherit pkgs; };
          two-node = import ./nix/tests/two-node.nix { inherit pkgs; };
        }
      );

      hydraJobs = self.checks;
      lib.mkBinary = mkBinary;

      nixosModules.default = import ./nix/module.nix;

      nixosConfigurations.deploytarget = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        modules = [
          ./nix/iso.nix
          {
            services.nix-connect.enable = true;
            services.nix-connect.kind = "live";
          }
        ];
      };
    };
}
