{ pkgs, ... }:

# v4.4.0 contains a commit that accidentally fixes the curses busy loop, which
# reduces CPU usage significantly:
# https://github.com/nicolargo/glances/commit/067eb918ad8ff0fb19c705ade98a0b69251e1558
let
  # Fetch a Nixpkgs with glances v4.4.1:
  # https://github.com/NixOS/nixpkgs/pull/470558/changes/93321a81a18221256e89d388f3a0e463522400f2
  glancesNixpkgs = pkgs.fetchFromGitHub {
    owner = "NixOS";
    repo = "nixpkgs";
    rev = "93321a81a18221256e89d388f3a0e463522400f2";
    hash = "sha256-ulO5Bq7RYu2ee0ckruo1HHFQfhzPTjv9noBtFezbX9U=";
  };
  glancesPkgs = import glancesNixpkgs { inherit (pkgs) system; };
  glances = glancesPkgs.glances.overrideAttrs (old: {
    # Reduce CPU usage even more by increasing the minimum main loop delay from
    # 0.1s to 1.0s:
    postPatch = (old.postPatch or "") + ''
      sed -i 's|delay=100|delay=1000|' glances/outputs/glances_curses.py
    '';
  });

  # Run on tty5 by default.
  tty = 5;
in

{
  systemd.services.glances-tty = {
    description = "run glances on tty${toString tty}";
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      ExecStart = "${glances}/bin/glances --disable-check-update";
      ExecStartPost = "+${pkgs.kbd}/bin/chvt ${toString tty}";
      # Force run as unprivileged user to prevent unauthorized access:
      DynamicUser = true;
      RuntimeDirectory = "glances-tty";
      TTYPath = "/dev/tty${toString tty}";
      TTYReset = true;
      TTYVTDisallocate = true;
      StandardInput = "null";
      StandardError = "journal";
      StandardOutput = "tty";
    };
    environment = {
      TERM = "xterm";
      HOME = "/run/glances-tty";
    };
  };
}
