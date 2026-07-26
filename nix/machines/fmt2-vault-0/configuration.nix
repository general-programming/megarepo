{
  self,
  inputs,
  lib,
  ...
}:

let
  inherit (inputs) disko;
in

{
  system.stateVersion = "26.05";

  imports = [
    disko.nixosModules.disko

    # Single-disk VM. If a given node has two disks, swap to
    # "disk/zfs-mirror" and the grub mirroredBoots layout from fmt2-core.
    (self.lib.nixosModule "disk/zfs-single")
    (self.lib.nixosModule "hardware/proxmox-vm")
    (self.lib.nixosModule "gitops")
    (self.lib.nixosModule "impermanence")
    (self.lib.nixosModule "vault-server")

    # Deliberately NOT imported: vault-agent / tailscale / holepunch — a
    # Vault server cannot consume vaultAgent (circular). See the module
    # header in nix/modules/vault-server/default.nix.
  ];

  impermanence.enable = true;

  # Canary node: tracks the `testing` branch so vault-server changes land
  # here first. Promote by merging to main (fmt2-vault-1/2 track main). See
  # the rollout section of docs/nix/vault-server.md. mkForce because
  # gitops.nix pins ref = "main" at normal priority.
  gitops = {
    enable = true;
    ref = lib.mkForce "testing";
  };

  vaultServer = {
    enable = true;
    nodeId = "fmt2-vault-0";
    ip = "10.65.67.24";
    peerIps = [
      "10.65.67.24"
      "10.65.67.25"
      "10.65.67.26"
    ];
  };

  networking = {
    hostName = "fmt2-vault-0";
    domain = "generalprogramming.org";
    # zfs pool guard; regenerate with `head -c4 /dev/urandom | od -An -tx1`.
    hostId = "951882d8";
    useDHCP = false;
  };

  # Single-disk => single ESP => systemd-boot (base.nix default is fine).

  # TODO(confirm): NIC name + default route for the actual VMs. Matching
  # en* covers ens18 (Proxmox virtio default) / eno1 / enp*. Static IP is
  # required because it is the cert SAN.
  systemd.network.enable = true;
  systemd.network.networks."10-primary" = {
    matchConfig.Name = "en*";
    address = [ "10.65.67.24/24" ];
    routes = [
      {
        Destination = "10.0.0.0/8";
        Gateway = "10.65.67.1";
      }
      # TODO(confirm): default route / upstream gateway for this segment.
      { Gateway = lib.mkDefault "10.65.67.1"; }
    ];
    linkConfig.RequiredForOnline = "routable";
  };
}
