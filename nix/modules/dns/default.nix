{
  modulesPath,
  lib,
  pkgs,
  ...
}:
let
    netbox_gen = builtins.fromJSON (builtins.readFile ./netbox.json);
    netbox_addresses = builtins.map ({fqdn, ip, ...}: "/${fqdn}/${ip}") netbox_gen.addresses;
    netbox_ptrs = builtins.map ({reverse_arpa, fqdn, ...}: "${reverse_arpa},${fqdn}") netbox_gen.ptr_records;
in
{
    networking.nameservers = ["127.0.0.1"];
    services.resolved.enable = false;
    services.dnsmasq = {
        enable = true;
        alwaysKeepRunning = true;
        settings = {
            server = [
                "/ipa.generalprogramming.org/10.3.0.4"
                "/ipa.generalprogramming.org/10.65.67.14"
                "/devhack.net/10.213.0.50"
                "/consul/127.0.0.1#8600"
                "1.1.1.1"
                "1.0.0.1"
            ];
            address = netbox_addresses;
            ptr-record = netbox_ptrs;
        };
    };

    # Expand the firewall too
    networking.firewall.allowedTCPPorts = [
        53
    ];

    networking.firewall.allowedUDPPorts = [
        53
    ];
}