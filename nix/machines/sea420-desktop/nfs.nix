{ ... }:
{
    # NFS server
    systemd.tmpfiles.rules = [
        "d /srv/nfs        0755 root root -"
        "d /srv/nfs/consw  0755 root root -"
    ];

    # Make consw a real mountpoint so NFSv4 crossmnt transitions into the
    # separate rw export. A subdirectory on the same filesystem is otherwise
    # served by the ro parent export, which is why writes fail with EROFS.
    fileSystems."/srv/nfs/consw" = {
        device = "/srv/nfs/consw";
        fsType = "none";
        options = [ "bind" ];
    };

    services.nfs.server = {
        enable = true;
        exports = ''
        /srv/nfs        10.36.75.0/24(ro,fsid=0,crossmnt,no_subtree_check)
        /srv/nfs/consw  10.36.75.0/24(rw,fsid=1,no_root_squash,no_subtree_check,async)
        '';
        # Pin the NFSv3 helper daemon ports so they can be firewalled.
        lockdPort = 4001;
        mountdPort = 4002;
        statdPort = 4000;
    };
    networking.firewall.allowedTCPPorts = [
        111   # rpcbind / portmapper
        2049  # nfsd
        4000  # statd
        4001  # lockd
        4002  # mountd
    ];
    networking.firewall.allowedUDPPorts = [
        111   # rpcbind / portmapper
        2049  # nfsd
        4000  # statd
        4001  # lockd
        4002  # mountd
    ];
}