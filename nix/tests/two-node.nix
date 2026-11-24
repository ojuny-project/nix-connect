{ pkgs }:
pkgs.testers.nixosTest {
  name = "nix-connect-two-node";

  nodes.admin =
    { ... }:
    {
      imports = [ ../module.nix ];

      services.nix-connect = {
        enable = true;
        discovery.auto = false;
        discovery.ranges = [ "192.168.1.0/24" ];
        discovery.interval = 1;
        initialAccount = "harness";
        members = [ "harness" ];
      };

      users.users.harness = {
        isNormalUser = true;
        extraGroups = [ "wheel" ];
      };

      networking.firewall.enable = false;
    };

  nodes.target =
    { ... }:
    {
      imports = [ ../module.nix ];

      services.nix-connect = {
        enable = true;
        discovery.auto = false;
        discovery.ranges = [ "192.168.1.0/24" ];
        discovery.interval = 1;
        initialAccount = "harness";
        members = [ "harness" ];
      };

      users.users.harness = {
        isNormalUser = true;
        extraGroups = [ "wheel" ];
      };

      networking.firewall.enable = false;
    };

  testScript = ''
    start_all()
    admin.wait_for_unit("nix-connect.service")
    target.wait_for_unit("nix-connect.service")
    admin.wait_for_open_port(5060)
    target.wait_for_open_port(5060)
    admin.wait_until_succeeds("nixconnect devices | grep -q target", timeout=30)
    target.wait_until_succeeds("nixconnect devices | grep -q admin", timeout=30)

    target_id = admin.succeed(
        "nixconnect devices | ${pkgs.jq}/bin/jq -r '.[] | select(.hostname == \"target\") | .id'"
    ).strip()
    admin_id = admin.succeed("nixconnect status | ${pkgs.jq}/bin/jq -r .id").strip()
    response = admin.succeed(f"nixconnect pair start {target_id}")
    pair_id = admin.succeed(
        f"echo '{response}' | ${pkgs.jq}/bin/jq -r .id"
    ).strip()
    code = admin.succeed(
        f"echo '{response}' | ${pkgs.jq}/bin/jq -r .code"
    ).strip()
    admin.succeed(f"nixconnect pair confirm {pair_id} {code}")
    target.succeed(
        f"nixconnect pair pending | ${pkgs.jq}/bin/jq -e '.[] | select(.id == \"{pair_id}\")'"
    )
    target.succeed(f"nixconnect pair confirm {pair_id} {code}")
    admin.succeed(
        f"nixconnect trust list | ${pkgs.jq}/bin/jq -e '.[] | select(.fingerprint == \"{target_id}\")'"
    )
    target.succeed(
        f"nixconnect trust list | ${pkgs.jq}/bin/jq -e '.[] | select(.fingerprint == \"{admin_id}\")'"
    )
    admin.succeed(f"nixconnect key share {target_id}")
    admin.succeed("nixconnect key rotate | grep -q ssh-ed25519")
    admin.succeed(f"nixconnect trust revoke {target_id}")
    admin.succeed("nixconnect trust list | ${pkgs.jq}/bin/jq -e 'length == 0'")
    target.succeed("nixconnect trust list | ${pkgs.jq}/bin/jq -e 'length == 0'")
  '';
}
