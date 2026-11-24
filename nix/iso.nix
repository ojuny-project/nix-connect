{ config, lib, pkgs, modulesPath, ... }:
let
  prepaired =
    if builtins.pathExists ../prepaired.json
    then builtins.fromJSON (builtins.readFile ../prepaired.json)
    else { };
in
{
  imports = [
    (modulesPath + "/installer/cd-dvd/installation-cd-minimal.nix")
    ./module.nix
  ];

  services.nix-connect = {
    enable = true;
    kind = "live";
    discovery.auto = true;
    openFirewall = true;
    prepairedFingerprint = prepaired.fingerprint or "";
    prepairedPublicKey = prepaired.publicKey or "";
    adminAuthorizedKey = prepaired.adminAuthorizedKey or "";
    adminAuthorizedKeyFile = "";
  };

  services.openssh = {
    enable = true;
    settings = {
      PermitRootLogin = "prohibit-password";
      PasswordAuthentication = false;
    };
  };

  networking.networkmanager.enable = true;
  services.getty.autologinUser = "nixos";

  users.users.nixos.extraGroups = [ "wheel" ];
  security.sudo.wheelNeedsPassword = false;

  system.stateVersion = "25.11";
}
