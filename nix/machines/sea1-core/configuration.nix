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

    (self.lib.nixosModule "disk/zfs-single")
    (self.lib.nixosModule "hardware/proxmox-vm")
    (self.lib.nixosModule "dns")
    (self.lib.nixosModule "gitops")
    (self.lib.nixosModule "glances-tty")
    (self.lib.nixosModule "cloudflared")
    (self.lib.nixosModule "dhcp")
    (self.lib.nixosModule "holepunch")
    (self.lib.nixosModule "impermanence")
    (self.lib.nixosModule "salt-master")
    (self.lib.nixosModule "tailscale")
    (self.lib.nixosModule "vault-agent")
    # (self.lib.nixosModule "network")
    # (self.lib.nixosModule "ssh")
    # secureboot/lanzaboote dropped: it can't sign comin's profile
    # namespace (see modules/secureboot.nix TODO), and gitops parity with
    # fmt2-core matters more on a salt master. Plain systemd-boot instead.

    ./consul.nix
  ];

  gitops = {
    enable = true;
    ref = "main";
  };

  # Hourly DNS/DHCP regeneration from NetBox, keyed via vault-agent.
  dns.refresh.enable = true;

  vaultAgent.enable = true;

  # Takes over from the legacy Ubuntu salt master on this IP once the box
  # is reprovisioned as NixOS; dormant (no creds) until `just provision`.
  saltMaster.enable = true;

  # Remaining legacy sea1-core services (see docs/nix/secrets.md for the
  # Vault paths each of these consumes).
  gpCloudflared.enable = true;
  holepunch.enable = true;

  gpTailscale.enable = true;
  services.tailscale = {
    # Exit node + advertised routes are host policy, kept out of the module.
    useRoutingFeatures = "server";
    extraSetFlags = [
      "--advertise-exit-node"
      "--advertise-routes=10.255.2.0/24"
    ];
  };

  # DHCP for the sea1 subnet, mirroring the legacy isc-dhcpd setup
  # (pool .3.128-.3.254, router .2.1, MTU 9000, 2h leases; v6 ::200-::fff).
  # Static reservations come from the dns module's NetBox refresh.
  dhcp = {
    enable = true;
    ranges = [
      "10.3.3.128,10.3.3.254,255.255.254.0,2h"
      "2602:fa6d:10:ffff::200,2602:fa6d:10:ffff::fff,116,2h"
    ];
    extraOptions = [
      "option:router,10.3.2.1"
      "option:dns-server,10.3.2.6"
      "option:mtu,9000"
      "option6:dns-server,[2602:fa6d:10:ffff::f00]"
    ];
  };

  networking = {
    hostName = "sea1-core";
    domain = "generalprogramming.org";
    hostId = "f7074b51";
  };

  # dnsmasq listens on the box's single internal-facing NIC (the vlan
  # names before this were a copy-paste from fmt2 and never existed here).
  services.dnsmasq = {
    settings.interface = [
      "enp6s18"
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
