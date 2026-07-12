{
  lib,
  pkgs,
  inputs,
  config,
  ...
}:

let
  inherit (inputs) lanzaboote;
in

{
  imports = [
    lanzaboote.nixosModules.lanzaboote
  ];

  services.fwupd.enable = true;

  boot = {
    # bootspec is always generated now; the enable option was removed.
    loader.systemd-boot.enable = lib.mkForce false;
    initrd = {
      systemd = {
        enable = true;
        tpm2.enable = true;
      };
    };
    lanzaboote = {
      enable = true;
      configurationLimit = 15;

      # TODO: upstream lanzaboote can't sign comin's profile namespace
      # (the old fork's profileGlob). Revisit before enabling gitops here.

      # Fix bug with sbctl. See:
      # https://github.com/nix-community/lanzaboote/issues/413
      pkiBundle = "/var/lib/sbctl";
    };
  };

  security.tpm2 = {
    enable = true;
    pkcs11.enable = true;
    tctiEnvironment.enable = true;
  };

  environment.systemPackages = with pkgs; [
    sbctl
  ];
}
