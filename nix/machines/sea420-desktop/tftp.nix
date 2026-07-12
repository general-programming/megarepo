{ ... }:
{
    systemd.tmpfiles.rules = [
        "d /srv/tftpboot  0755 root root -"
    ];

    services.atftpd = {
        enable = true;
        root = "/srv/tftpboot";
    };

    networking.firewall.allowedUDPPorts = [ 69 ];
}
