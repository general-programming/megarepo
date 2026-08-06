# sea420-core: DNS + DHCP + consul server for the sea420 site, replacing the
# Salt-managed Ubuntu LXC (CT 1003 on the sea420 PVE). Stays an LXC; services
# mirror sea1-core minus its extras
# (no tailscale/cloudflared/holepunch/salt-master here).
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

    (self.lib.nixosModule "dns")
    (self.lib.nixosModule "gitops")
    (self.lib.nixosModule "kea")
    (self.lib.nixosModule "vault-agent")

    ./consul.nix
  ];

  gitops = {
    enable = true;
    ref = "main";
  };

  vaultAgent.enable = true;

  # Hourly DNS regeneration from NetBox, replacing Salt's 99-dns.conf render.
  dns.refresh.enable = true;

  # Both site-facing NICs: internal clients query 10.3.0.3 directly; net1
  # clients (10.3.6.0/27) reach it routed, arriving on net1.
  services.dnsmasq.settings.interface = [
    "internal"
    "net1"
  ];

  # DHCP via Kea, replacing isc-dhcpd. Same shape: internal pool
  # .1.128-.1.254, 2h leases; the net1-external shared-network carries the
  # private /27 and the public /24 on one wire. NetBox reservations and the
  # Discord lease webhook come with the module (parity with dhcpd's
  # 10-netbox.conf include and on-commit hook).
  kea = {
    enable = true;
    interfaces = [
      "internal"
      "net1"
    ];
    dhcp4.subnets = [
      {
        id = 1;
        subnet = "10.3.0.0/23";
        pools = [ { pool = "10.3.1.128 - 10.3.1.254"; } ];
        option-data = [
          { name = "routers"; data = "10.3.0.1"; }
          { name = "domain-name-servers"; data = "10.3.0.3, 10.65.67.5"; }
          { name = "domain-name"; data = "generalprogramming.org"; }
        ];
        valid-lifetime = 7200;
      }
    ];
    dhcp4.sharedNetworks = [
      {
        name = "net1-external";
        interface = "net1";
        subnet4 = [
          {
            id = 2;
            subnet = "10.3.6.0/27";
            option-data = [
              { name = "routers"; data = "10.3.6.1"; }
              { name = "domain-name-servers"; data = "10.3.0.3, 10.65.67.5"; }
            ];
            valid-lifetime = 7200;
          }
          {
            id = 3;
            subnet = "209.251.245.0/24";
            option-data = [
              { name = "routers"; data = "209.251.245.254"; }
              { name = "domain-name-servers"; data = "1.1.1.1"; }
            ];
            valid-lifetime = 7200;
          }
        ];
      }
    ];
  };

  networking = {
    domain = "generalprogramming.org";
    useDHCP = false;
  };

  proxmoxLXC = {
    # Proxmox's net/hostname injection doesn't know NixOS; configure both
    # from Nix instead.
    manageNetwork = false;
    manageHostName = true;
  };

  # Interface names are the PVE-assigned ones (internal/net1), which are
  # primary kernel names inside the CT — dnsmasq and Kea match on them.
  systemd.network.networks = {
    "10-internal" = {
      matchConfig.Name = "internal";
      address = [ "10.3.0.3/23" ];
      routes = [ { Gateway = "10.3.0.1"; } ];
      # v6 is RA-assigned (site ULA), same as the Ubuntu box.
      networkConfig.IPv6AcceptRA = true;
      linkConfig.RequiredForOnline = "routable";
    };
    "20-net1" = {
      matchConfig.Name = "net1";
      address = [ "10.3.6.2/27" ];
      networkConfig.IPv6AcceptRA = false;
    };
  };

  # Container: no bootloader/firmware/kernel to manage, and no raw sockets
  # for lldpd under an unprivileged LXC.
  boot.loader.systemd-boot.enable = lib.mkForce false;
  boot.loader.efi.canTouchEfiVariables = lib.mkForce false;
  hardware.enableRedistributableFirmware = lib.mkForce false;
  hardware.cpu.intel.updateMicrocode = lib.mkForce false;
  hardware.cpu.amd.updateMicrocode = lib.mkForce false;
  services.lldpd.enable = lib.mkForce false;
}
