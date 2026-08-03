{
  modulesPath,
  lib,
  self,
  ...
}:

{
  system.stateVersion = "26.05"; # do not change

  imports = [
    (modulesPath + "/virtualisation/proxmox-lxc.nix")
    (self.lib.nixosModule "gitops")
  ];

  gitops = {
    enable = false;
    ref = "main";
  };

  networking = {
    hostName = "pipebomb";
    domain = "generalprogramming.org";
  };

  proxmoxLXC = {
    # manageNetwork = false enables systemd-networkd but ships no matching
    # .network file for eth0 (Proxmox's own net-injection only covers
    # distros it recognizes, and "nixos" isn't one), so the interface has
    # to be configured here. Same story for the hostname: leaving
    # manageHostName = true lets Nix set it from the machine name.
    manageNetwork = false;
    manageHostName = true;
  };

  systemd.network.networks."10-eth0" = {
    matchConfig.Name = "eth0";
    networkConfig.DHCP = "yes";
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

  # No glances-tty module either: it wants a real /dev/tty5, which the
  # container does not have. `ssh pipebomb -t glances` still works — the
  # package comes in fleet-wide from base.nix.
}
