{ pkgs }:
pkgs.testers.nixosTest {
  name = "nix-connect-single-node";

  nodes.machine =
    { ... }:
    {
      imports = [ ../module.nix ];

      services.nix-connect = {
        enable = true;
        discovery.auto = false;
        initialAccount = "harness";
        members = [ "harness" ];
        adminAuthorizedKeyFile = toString ./admin_ssh.pub;
      };

      users.users.harness = {
        isNormalUser = true;
        extraGroups = [ "wheel" ];
      };

      networking.firewall.enable = false;
      services.openssh.enable = true;
    };

  testScript = ''
    machine.wait_for_unit("nix-connect.service")
    machine.wait_for_open_port(5060)
    machine.wait_for_open_port(5061)
    machine.succeed("${pkgs.curl}/bin/curl -fsS http://127.0.0.1:5060/.nix-connect/v1/info")
    machine.succeed("nixconnect status")
    machine.succeed("grep -q 'test@nix-connect' /etc/ssh/authorized_keys.d/harness")
  '';
}
