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
    (self.lib.nixosModule "salt-master")
    (self.lib.nixosModule "vault-agent")
    # (self.lib.nixosModule "network")
    # (self.lib.nixosModule "ssh")
    (self.lib.nixosModule "secureboot")

    ./consul.nix
  ];

  gitops = {
    enable = false;
    ref = "main";
  };

  vaultAgent.enable = true;

  # Takes over from the legacy Ubuntu salt master on this IP once the box
  # is reprovisioned as NixOS; dormant (no creds) until `just provision`.
  saltMaster.enable = true;

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
          { Gateway = "10.3.2.1"; }
        ];

        # Static v6 address, but the default route comes from SLAAC/RA.
        networkConfig.IPv6AcceptRA = true;

        linkConfig.RequiredForOnline = "routable";
      };
    };
  };
}
