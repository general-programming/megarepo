{
  lib,
  pkgs,
  config,
  ...
}:

# Shared patched glances, installed fleet-wide so `ssh <host> -t glances`
# shows the same view as the tty dashboard (see glances-tty.nix).
#
# glances v4.4.0+ contains a commit that accidentally fixes the curses busy
# loop, which reduces CPU usage significantly:
# https://github.com/nicolargo/glances/commit/067eb918ad8ff0fb19c705ade98a0b69251e1558
# Nixpkgs is now well past that version, so patch its glances directly.
let
  glances = pkgs.glances.overrideAttrs (old: {
    # Reduce CPU usage even more by increasing the minimum main loop delay from
    # 0.1s to 1.0s:
    postPatch = (old.postPatch or "") + ''
      sed -i 's|delay=100|delay=1000|' glances/outputs/glances_curses.py
    '';
    # The patch forces a from-source rebuild, and 4.5.5's RESTful tests
    # (spinning up the web server on localhost) fail in the build sandbox.
    # The patch only touches the curses delay, so skip the suite.
    doCheck = false;
    doInstallCheck = false;
  });
in

{
  options.gpGlances.package = lib.mkOption {
    type = lib.types.package;
    default = glances;
    description = "Pinned/patched glances used both interactively and by glances-tty.";
  };

  config.environment.systemPackages = [ config.gpGlances.package ];
}
