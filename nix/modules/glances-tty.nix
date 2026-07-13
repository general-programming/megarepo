{ pkgs, config, ... }:

# Persistent glances dashboard on a virtual terminal. The pinned/patched
# glances package itself lives in glances.nix (imported fleet-wide via
# base.nix) so `ssh <host> -t glances` shows the same view.
let
  # Run on tty5 by default.
  tty = 5;
in

{
  imports = [ ./glances.nix ];

  systemd.services.glances-tty = {
    description = "run glances on tty${toString tty}";
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      ExecStart = "${config.gpGlances.package}/bin/glances --disable-check-update";
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
