{
  self,
  inputs,
  ...
}:

let
  inherit (inputs)
    disko
    ;
in

{
  system.stateVersion = "26.05"; # do not change

  imports = [
    disko.nixosModules.disko

    (self.lib.nixosModule "hardware/proxmox-vm")
    (self.lib.nixosModule "glances-tty")
    (self.lib.nixosModule "gitops")
    (self.lib.nixosModule "impermanence")
    # No secureboot module: this VM boots EFI with Secure Boot disabled, so
    # plain systemd-boot from base.nix is what we want.

    ./disko.nix
  ];

  gitops = {
    enable = false;
    ref = "main";
  };

  networking = {
    hostName = "pipebomb";
    domain = "generalprogramming.org";
    useDHCP = false;
    # Required by ZFS.
    hostId = "f1bc0b56";
  };

  # Ephemeral root: zroot/root is rolled back to @blank in initrd, and
  # /persist has to be mounted before the bind mounts and activation run.
  impermanence.enable = true;
  fileSystems."/persist".neededForBoot = true;

  # Daemon state, on top of the module's defaults (/var/lib, /var/log,
  # /root). /var/lib/daemon already rides along inside the /var/lib bind;
  # listing it explicitly just pins its existence and mode under /persist.
  impermanence.extraPersistDirectories = [
    {
      path = /etc/daemon;
      mode = "0755";
      owner = "root";
      group = "root";
    }
    {
      path = /var/lib/daemon;
      mode = "0755";
      owner = "root";
      group = "root";
    }
  ];

  # Single NIC, DHCP. Matches both plausible predictable names for the
  # Proxmox virtio NIC: the live installer brings it up as ens18 (i440fx
  # SMBIOS onboard index), while a q35 board names the same device enp6s18.
  systemd.network.enable = true;
  systemd.network.networks."10-primary" = {
    matchConfig.Name = [
      "enp6s18"
      "ens18"
    ];
    networkConfig.DHCP = "yes";
    linkConfig.RequiredForOnline = "routable";
  };
}
