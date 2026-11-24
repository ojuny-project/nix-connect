{ pkgs }:
let
  version = "0.1.0";
  src = pkgs.lib.cleanSource ../.;

  runtimeTools = with pkgs; [
    coreutils
    findutils
    git
    gnugrep
    gnused
    nix
    openssh
    pamtester
    rsync
    sudo
    util-linux
    deploy-rs
    disko
    nixos-anywhere
  ];

  wrapRuntime = ''
    if [ -d "$out/bin" ]; then
      for program in "$out"/bin/*; do
        if [ -f "$program" ] && [ -x "$program" ]; then
          wrapProgram "$program" --prefix PATH : ${pkgs.lib.makeBinPath runtimeTools}
        fi
      done
    fi
  '';

  common = {
    inherit version src;
    vendorHash = "sha256-fChH7+8nZvJst4sG6RFDLWyESblTpKcgyQN62V87JB4=";
    doCheck = true;
    nativeBuildInputs = [ pkgs.makeWrapper ];
    nativeCheckInputs = [ pkgs.git ];
    postFixup = wrapRuntime;
  };
in
{
  nixconnect-daemon = pkgs.buildGoModule (common // {
    pname = "nixconnect-daemon";
    subPackages = [ "cmd/nixconnect-daemon" ];
    meta = {
      mainProgram = "nixconnect-daemon";
    };
  });

  nixconnect = pkgs.buildGoModule (common // {
    pname = "nixconnect";
    subPackages = [ "cmd/nixconnect" ];
    meta = {
      mainProgram = "nixconnect";
    };
    postInstall = ''
      ln -s nixconnect $out/bin/ncon
    '';
  });

  nixconnect-tests = pkgs.buildGoModule (common // {
    pname = "nixconnect-tests";
    subPackages = [ "cmd/nixconnect-daemon" ];
    doCheck = true;
    checkPhase = ''
      go test ./internal/...
    '';
    postFixup = "";
  });

  nixconnect-gui = pkgs.buildGoModule (common // {
    pname = "nixconnect-gui";
    subPackages = [ "cmd/nixconnect-gui" ];
    meta = {
      mainProgram = "nixconnect-gui";
    };
    doCheck = false;
    env = {
      CGO_ENABLED = "1";
    };
    nativeBuildInputs = [
      pkgs.makeWrapper
      pkgs.pkg-config
      pkgs.gobject-introspection
      pkgs.wrapGAppsHook4
    ];
    buildInputs = [
      pkgs.glib
      pkgs.gobject-introspection
      pkgs.gtk4
      pkgs.libadwaita
    ];
    postInstall = ''
      install -Dm644 ${../org.ojuny.NixConnect.desktop} \
        $out/share/applications/org.ojuny.NixConnect.desktop
    '';
    postFixup = wrapRuntime;
  });

  nixconnect-source = pkgs.runCommand "nixconnect-source" { } ''
    mkdir -p $out
    cp -r ${src}/. $out/
  '';
}
