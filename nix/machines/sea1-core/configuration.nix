{
  self,
  inputs,
  config,
  ...
}:

let
  inherit (inputs)
    disko
    ;
in

{
  system.stateVersion = "26.05";

  imports = [
    disko.nixosModules.disko

    (self.lib.nixosModule "disk/zfs-mirror")
    (self.lib.nixosModule "hardware/proxmox-vm")
    (self.lib.nixosModule "dns")
    (self.lib.nixosModule "gitops")
    (self.lib.nixosModule "glances-tty")
    (self.lib.nixosModule "impermanence")
    # (self.lib.nixosModule "network")
    # (self.lib.nixosModule "ssh")
    (self.lib.nixosModule "secureboot")

    ./consul.nix
  ];

  gitops = {
    enable = false;
    ref = "main";
  };

  networking = {
    hostName = "sea1-core";
    domain = "generalprogramming.org";
    hostId = "f7074b51";
  };

  # dnsmasq
  services.dnsmasq = {
    settings.interface = [
      "vlan5"
      "vlan1000"
    ];
  };

  # Networking
  networking.useDHCP = false;

  # Primary is eno1, uses DHCP
  systemd.network.enable = true;

  systemd.network = {
    networks = {
      # Primary is enp6s18
      "10-primary" = {
        matchConfig.Name = "enp6s18";
        address = [
          "10.3.2.6/23"
          "2602:fa6d:10:ffff::f00/116"
        ];

        routes = [
          { Gateway = "2602:fa6d:10:ffff::1"; }
          { Gateway = "10.3.2.1"; }
        ];

        linkConfig.RequiredForOnline = "routable";
      };
    };
  };
}
