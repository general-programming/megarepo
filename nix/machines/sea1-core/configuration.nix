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
    (self.lib.nixosModule "kea")
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

  # Plain tailnet client: the k8s tailscale connectors own exit-node and
  # subnet-router duties for sea1. Explicit =false/empty flags so
  # `tailscale set` clears anything advertised previously.
  gpTailscale.enable = true;
  services.tailscale.extraSetFlags = [
    "--advertise-exit-node=false"
    "--advertise-routes="
  ];

  # DHCP for the sea1 subnet via Kea (Stage 2: dnsmasq keeps DNS only).
  # Same shape as the old dnsmasq/isc-dhcpd setup: pool .3.128-.3.254,
  # router .2.1, MTU 9000, 2h leases; v6 pool ::200-::fff. Reservations are
  # MAC-keyed for both families and rendered hourly from NetBox.
  kea = {
    enable = true;
    interfaces = [ "ens18" ];
    dhcp4.subnets = [
      {
        id = 1;
        subnet = "10.3.2.0/23";
        pools = [ { pool = "10.3.3.128 - 10.3.3.254"; } ];
        option-data = [
          { name = "routers"; data = "10.3.2.1"; }
          { name = "domain-name-servers"; data = "10.3.2.6"; }
          { name = "interface-mtu"; data = "9000"; }
        ];
        valid-lifetime = 7200;
      }
    ];
    dhcp6 = {
      enable = true;
      subnets = [
        {
          id = 1;
          subnet = "2602:fa6d:10:ffff::/64";
          # DHCPv6 clients talk from link-local sources; Kea needs the
          # explicit interface binding to select this subnet for them.
          interface = "ens18";
          # Stops at ::eff so the whole ::f00-::fff block is reserved for
          # infrastructure and can never be handed to a DHCPv6 client. Four
          # addresses were already exposed by the old ::fff bound -- this
          # host's own ::f00, sea1-vpn-leaf-1 ::f01, sea1-vpn-spine-1 ::f02
          # and sea1-vpn-spine-2 ::f13 -- and survived only because kea
          # allocates from the low end and there are three leases in use.
          # sea1-vpn-leaf-2 at ::f06 would have joined them.
          pools = [ { pool = "2602:fa6d:10:ffff::200 - 2602:fa6d:10:ffff::eff"; } ];
          option-data = [
            { name = "dns-servers"; data = "2602:fa6d:10:ffff::f00"; }
          ];
          valid-lifetime = 7200;
        }
      ];
    };
  };

  networking = {
    hostName = "sea1-core";
    domain = "generalprogramming.org";
    hostId = "f7074b51";
  };

  # dnsmasq listens on the box's single internal-facing NIC. Must be the
  # PRIMARY kernel name: dnsmasq does not match altnames (enp6s18 is an
  # altname of ens18 on this VM), and a non-matching filter silently
  # serves loopback only.
  services.dnsmasq = {
    settings.interface = [
      "ens18"
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
