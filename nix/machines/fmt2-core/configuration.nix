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
    # (self.lib.nixosModule "secureboot")

    ./consul.nix
  ];

  gitops = {
    enable = false;
    ref = "main";
  };

  networking = {
    hostName = "fmt2-core";
    domain = "generalprogramming.org";
    hostId = "31ad426c";
  };

  boot.loader.systemd-boot = {
    enable = false;
  };

  boot.loader.grub = {
    enable = true;
    efiSupport = true;

    mirroredBoots = [
      { path = "/boot"; devices = [ "nodev" ]; }
      { path = "/boot1"; devices = [ "nodev" ]; }
    ];
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
    netdevs = {
      "20-vlan5" = {
        netdevConfig = {
          Kind = "vlan";
          Name = "vlan5";
        };
        vlanConfig.Id = 5;
      };

      "20-vlan1000" = {
        netdevConfig = {
          Kind = "vlan";
          Name = "vlan1000";
        };
        vlanConfig.Id = 1000;
      };
    };

    networks = {
      # Primary is eno1, with dhcpv4, separate vrf, no dhcpv6
      "10-primary" = {
        matchConfig.Name = "eno1";
        address = [
          "79.110.170.3/24"
        ];

        routes = [
          { Gateway = "79.110.170.1"; }
        ];

        vlan = [
          "vlan5"
          "vlan1000"
        ];

        linkConfig.RequiredForOnline = "routable";
      };


      # Secondary is eno2, no dhcpv4, separate vrf, with dhcpv6
      "10-secondary" = {
         matchConfig.Name = "eno2";
        networkConfig = {
          DHCP = "ipv6";
        };
      };

      # VLANs
      "20-vlan5" = {
        matchConfig.Name = "vlan5";
        address = [
          "10.65.67.5/24"
        ];

        routes = [
          {
            Destination = "10.0.0.0/8";
            Gateway = "10.65.67.1";
          }
        ];
      };

      "20-vlan1000" = {
        matchConfig.Name = "vlan1000";
        address = [
          "10.255.1.9/24"
        ];
      };
    };
  };
}
