{ pkgs, lib, config, inputs, ... }:

{
  # https://devenv.sh/basics/
  env.GREET = "devenv";

  # https://devenv.sh/packages/
  packages = with pkgs; [
    coder
    git
    talosctl
    # machine provisioning (nix/justfile)
    just
    vault
  ];

  # https://devenv.sh/languages/python/
  languages.python = {
    enable = true;
    venv.enable = true;
    uv = {
      enable = true;
      sync.enable = true;
    };
  };
}
