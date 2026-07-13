{
  modulesPath,
  lib,
  self,
  ...
}:

{
  system.stateVersion = "26.05";

  imports = [
    (modulesPath + "/virtualisation/proxmox-lxc.nix")
    (self.lib.nixosModule "gitops")
  ];

  gitops = {
    enable = true;
    ref = "main";
  };

  proxmoxLXC = {
    # manageNetwork = false enables systemd-networkd but ships no matching
    # .network file for eth0 (Proxmox's own net-injection only covers
    # distros it recognizes, and "nixos" isn't one), so the interface never
    # got configured. Static v6 (fleet convention here has no v6 DHCP) +
    # DHCP for v4.
    manageNetwork = false;
    # Same story as manageNetwork: Proxmox's hostname injection doesn't
    # cover NixOS, and manageHostName = false force-blanks
    # networking.hostName. Let Nix set it (the flake wires it to the
    # machine name, sea1-nix-builder).
    manageHostName = true;
  };

  systemd.network.networks."10-eth0" = {
    matchConfig.Name = "eth0";
    address = [ "2602:fa6d:10:ffff::f14/116" ];
    networkConfig.DHCP = "ipv4";
    routes = [ { Gateway = "2602:fa6d:10:ffff::1"; } ];
    linkConfig.RequiredForOnline = "routable";
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
