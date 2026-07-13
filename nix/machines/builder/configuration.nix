{
  modulesPath,
  lib,
  ...
}:

{
  system.stateVersion = "26.05";

  imports = [
    (modulesPath + "/virtualisation/proxmox-lxc.nix")
  ];

  proxmoxLXC = {
    manageNetwork = false;
    manageHostName = false;
  };

  # Container: no real bootloader/firmware/kernel to manage, and no raw
  # sockets for lldpd under an unprivileged LXC.
  boot.loader.systemd-boot.enable = lib.mkForce false;
  boot.loader.efi.canTouchEfiVariables = lib.mkForce false;
  hardware.enableRedistributableFirmware = lib.mkForce false;
  hardware.cpu.intel.updateMicrocode = lib.mkForce false;
  hardware.cpu.amd.updateMicrocode = lib.mkForce false;
  services.lldpd.enable = lib.mkForce false;

  # Remote-build user. Only the public half lives here; the matching
  # private key is minted once and stored in Vault, then rendered to
  # dispatching hosts via vault-agent (see nix-builder-client.nix).
  users.users.builder = {
    isNormalUser = true;
    description = "Nix remote build user";
    openssh.authorizedKeys.keys = [
      # Private half in Vault: secret/app/nix-builder-ssh (see
      # nix-builder-client.nix for how dispatching hosts consume it).
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHd0ik2Dotavt0lpycowB+IPE5hhx/8sk4BGTvFHEN5v nix-builder"
    ];
  };

  nix.settings.trusted-users = [ "builder" ];

  # It can't remote-build to itself.
  nixBuilder.enable = lib.mkForce false;
}
