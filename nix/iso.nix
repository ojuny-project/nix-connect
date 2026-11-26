{ config, lib, pkgs, modulesPath, ... }:
let
  prepaired =
    if builtins.pathExists ../prepaired.json
    then builtins.fromJSON (builtins.readFile ../prepaired.json)
    else { };
  shortID = builtins.substring 0 8 (prepaired.fingerprint or "");
  imageName = if shortID == "" then "deploytarget" else "deploytarget-${shortID}";
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

  image.baseName = lib.mkForce imageName;

  system.stateVersion = "25.11";
}
