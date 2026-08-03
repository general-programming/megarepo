# Single-disk ZFS root laid out for impermanence: `zroot/root` is rolled
# back to the `@blank` snapshot in initrd on every boot (see
# modules/impermanence.nix), so anything that must survive lives on
# `zroot/persist` and is bind-mounted back in. Modelled on
# machines/sea420-desktop/disko.nix, minus the LUKS layer.
#
# The shared modules/disk/zfs-single.nix is deliberately not used here: it
# puts the root filesystem on the pool itself and snapshots `zroot@blank`,
# while the impermanence module rolls back `zroot/root@blank`.
{ ... }:

{
  # 26.11 default; the pool stays importable without -f as long as the hostid
  # matches the last importer, which is always this same machine. Set here
  # rather than inherited, since modules/disk/zfs-single.nix is not imported.
  boot.zfs.forceImportRoot = false;

  disko.devices = {
    disk.disk0 = {
      device = "/dev/sda";
      type = "disk";
      content = {
        type = "gpt";
        partitions = {
          ESP = {
            type = "EF00";
            size = "512M";
            content = {
              type = "filesystem";
              format = "vfat";
              mountpoint = "/boot";
              mountOptions = [ "umask=0077" ];
            };
          };
          zfs = {
            size = "100%";
            content = {
              type = "zfs";
              pool = "zroot";
            };
          };
        };
      };
    };

    zpool.zroot = {
      type = "zpool";
      options = {
        ashift = "12";
        # Workaround: cannot import 'zroot': I/O error in disko tests
        cachefile = "none";
      };
      rootFsOptions = {
        acltype = "posixacl";
        atime = "off";
        compression = "zstd";
        mountpoint = "none";
        xattr = "sa";
        "com.sun:auto-snapshot" = "false";
      };
      datasets = {
        root = {
          type = "zfs_fs";
          mountpoint = "/";
          options.mountpoint = "legacy";
          # The rollback target. Taken while the dataset is still empty,
          # so every boot returns to a pristine root.
          postCreateHook = "zfs list -t snapshot -H -o name | grep -qE '^zroot/root@blank$' || zfs snapshot zroot/root@blank";
        };
        nix = {
          type = "zfs_fs";
          mountpoint = "/nix";
          options.mountpoint = "legacy";
        };
        persist = {
          type = "zfs_fs";
          mountpoint = "/persist";
          options.mountpoint = "legacy";
        };
      };
    };
  };
}
