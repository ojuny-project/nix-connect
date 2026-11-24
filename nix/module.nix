{ config, lib, pkgs, ... }:
let
  cfg = config.services.nix-connect;
  packages = import ./packages.nix { inherit pkgs; };

  trustRecord = lib.optionalAttrs (cfg.prepairedFingerprint != "") {
    ${cfg.prepairedFingerprint} = {
      fingerprint = cfg.prepairedFingerprint;
      publicKey = cfg.prepairedPublicKey;
      hostname = "administration";
      pairedAt = "1970-01-01T00:00:00Z";
    };
  };

  daemonArguments = [
    "--state-dir" cfg.stateDir
    "--info-port" (toString cfg.infoPort)
    "--control-port" (toString cfg.controlPort)
    "--socket" cfg.socket
    "--agent-socket" cfg.agentSocket
    "--kind" cfg.kind
    "--account" cfg.initialAccount
    "--ssh-user" cfg.sshUser
    "--ssh-port" (toString cfg.sshPort)
    "--pam-service" cfg.pamService
    "--discovery-auto=${lib.boolToString cfg.discovery.auto}"
    "--discovery-interval" (toString cfg.discovery.interval)
    "--discovery-timeout" (toString cfg.discovery.timeout)
    "--discovery-workers" (toString cfg.discovery.workers)
    "--magic-rollback=${lib.boolToString cfg.magicRollback.enable}"
    "--magic-mode" cfg.magicRollback.mode
    "--magic-auto-timeout" (toString cfg.magicRollback.automaticTimeout)
    "--magic-manual-timeout" (toString cfg.magicRollback.manualTimeout)
    "--iso-source" cfg.isoSource
  ] ++ lib.concatMap (range: [ "--discovery-range" range ]) cfg.discovery.ranges;
in
{
  options.services.nix-connect = {
    enable = lib.mkEnableOption "nix-connect";

    kind = lib.mkOption {
      type = lib.types.enum [ "installed" "live" ];
      default = "installed";
      description = "Device kind advertised on the public plane.";
    };

    package = lib.mkOption {
      type = lib.types.package;
      default = packages.nixconnect-daemon;
    };

    cliPackage = lib.mkOption {
      type = lib.types.package;
      default = packages.nixconnect;
    };

    guiPackage = lib.mkOption {
      type = lib.types.package;
      default = packages.nixconnect-gui;
    };

    cli.enable = lib.mkOption {
      type = lib.types.bool;
      default = true;
    };

    gui.enable = lib.mkOption {
      type = lib.types.bool;
      default = false;
    };

    isoSource = lib.mkOption {
      type = lib.types.path;
      default = packages.nixconnect-source;
      description = "nix-connect flake source used to build the deploytarget ISO.";
    };

    infoPort = lib.mkOption {
      type = lib.types.port;
      default = 5060;
    };

    controlPort = lib.mkOption {
      type = lib.types.port;
      default = 5061;
    };

    socket = lib.mkOption {
      type = lib.types.str;
      default = "/run/nix-connect/control.sock";
    };

    agentSocket = lib.mkOption {
      type = lib.types.str;
      default = "/run/nix-connect/agent.sock";
    };

    stateDir = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/nix-connect";
    };

    initialAccount = lib.mkOption {
      type = lib.types.str;
      default = "nixos";
      description = "Account advertised as the default SSH and consent account.";
    };

    members = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ cfg.initialAccount ];
      description = "Accounts allowed to use the local control and agent sockets.";
    };

    sshUser = lib.mkOption {
      type = lib.types.str;
      default = cfg.initialAccount;
    };

    sshPort = lib.mkOption {
      type = lib.types.port;
      default = 22;
    };

    pamService = lib.mkOption {
      type = lib.types.str;
      default = "nixconnect";
    };

    discovery = {
      auto = lib.mkOption {
        type = lib.types.bool;
        default = true;
      };
      ranges = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
      };
      interval = lib.mkOption {
        type = lib.types.int;
        default = 60;
      };
      timeout = lib.mkOption {
        type = lib.types.int;
        default = 2;
      };
      workers = lib.mkOption {
        type = lib.types.int;
        default = 64;
      };
    };

    trustedNetworks = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
    };

    publicIpUrl = lib.mkOption {
      type = lib.types.str;
      default = "";
    };

    magicRollback = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
      };
      mode = lib.mkOption {
        type = lib.types.enum [ "automatic" "manual" ];
        default = "automatic";
      };
      automaticTimeout = lib.mkOption {
        type = lib.types.int;
        default = 60;
      };
      manualTimeout = lib.mkOption {
        type = lib.types.int;
        default = 600;
      };
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = true;
    };

    prepairedFingerprint = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Administration device identity paired at image build time.";
    };

    prepairedPublicKey = lib.mkOption {
      type = lib.types.str;
      default = "";
    };

    adminAuthorizedKey = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Administration SSH public key installed for the initial account.";
    };

    adminAuthorizedKeyFile = lib.mkOption {
      type = lib.types.str;
      default = "${cfg.stateDir}/admin_ssh.pub";
      description = "File containing the administration SSH public key authorized for the initial account.";
    };
  };

  config = lib.mkIf cfg.enable {
    users.groups.nix-connect = { };

    systemd.services.nix-connect = {
      description = "nix-connect daemon";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      preStart = lib.mkIf (cfg.prepairedFingerprint != "") ''
        if [ ! -e ${cfg.stateDir}/trust.json ]; then
          cat > ${cfg.stateDir}/trust.json <<'EOF'
${builtins.toJSON trustRecord}
EOF
          chmod 0600 ${cfg.stateDir}/trust.json
        fi
      '';
      serviceConfig = {
        ExecStart = "${cfg.package}/bin/nixconnect-daemon ${lib.escapeShellArgs daemonArguments}";
        Group = "nix-connect";
        StateDirectory = "nix-connect";
        RuntimeDirectory = "nix-connect";
        RuntimeDirectoryMode = "0750";
        Restart = "on-failure";
      };
    };

    users.users = lib.genAttrs (lib.unique (cfg.members ++ [ cfg.initialAccount ])) (name: {
      extraGroups = [ "nix-connect" ];
      openssh.authorizedKeys.keys = lib.filter (key: key != "") (
        lib.unique (
          lib.optional (name == cfg.initialAccount && cfg.adminAuthorizedKey != "") cfg.adminAuthorizedKey
          ++ lib.optional
            (
              name == cfg.initialAccount
              && cfg.adminAuthorizedKeyFile != ""
              && builtins.pathExists cfg.adminAuthorizedKeyFile
            )
            (lib.trim (builtins.readFile cfg.adminAuthorizedKeyFile))
        )
      );
    });

    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedTCPPorts = [ cfg.infoPort cfg.controlPort ];
    };

    security.pam.services.${cfg.pamService} = { };

    environment.systemPackages =
      lib.optional cfg.cli.enable cfg.cliPackage
      ++ lib.optional cfg.gui.enable cfg.guiPackage;
  };
}
