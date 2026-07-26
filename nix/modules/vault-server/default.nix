# Declarative Vault server (HashiCorp Vault, integrated Raft storage).
#
# Replaces the three hand-provisioned Vault VMs (10.65.67.24/25/26) whose
# only record was the manual runbook in the repo README. Each node is a
# raft peer; the cluster is reinstalled in place one node at a time so the
# surviving two keep quorum and the existing vault-proxy.catgirls.dev
# front (which targets the same IPs) never notices. See
# docs/nix/vault-server.md for the migration runbook.
#
# Trust model:
#   - TLS leaf certs are minted off-box: a software RSA key + CSR signed by
#     the offline Nitrokey HSM intermediate CA (~/nocloud/hsm, `just
#     generate-vault` / `bin/gen_vault <IP>`). SANs are IP-based
#     (IP:<node>, IP:127.0.0.1, DNS:localhost), so raft peers and api_addr
#     MUST address each other by IP, not DNS. Nix only references the cert
#     PATHS; the key material is scp'd to the node like the vault-agent
#     AppRole is seeded out-of-band.
#   - Unseal is transit auto-unseal. The `seal "transit"` stanza (with its
#     token) lives in a seeded file at sealSettingsPath, NOT in the Nix
#     store, and is merged at runtime via services.vault.extraSettingsPaths.
#
# Hard rule: these hosts MUST NOT set vaultAgent.enable — a Vault server
# cannot fetch its own secrets through vault-agent (circular). That also
# means the gpTailscale Vault-delivered auth-key path does not apply here;
# join the tailnet out-of-band if/when the tailnet-only phase lands.

{
  lib,
  config,
  ...
}:

let
  cfg = config.vaultServer;
in
{
  options.vaultServer = {
    enable = lib.mkEnableOption "Vault server (raft member)";

    nodeId = lib.mkOption {
      type = lib.types.str;
      example = "fmt2-vault-0";
      description = "Raft node_id for this member. Must be unique in the cluster.";
    };

    ip = lib.mkOption {
      type = lib.types.str;
      example = "10.65.67.24";
      description = ''
        This node's IP, as it appears in the node cert's IP SAN. Used to
        build api_addr/cluster_addr, so it must match a SAN or peers reject
        the TLS handshake.
      '';
    };

    peerIps = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      example = [
        "10.65.67.24"
        "10.65.67.25"
        "10.65.67.26"
      ];
      description = ''
        All raft member IPs (this node included — Vault skips itself). Each
        becomes a retry_join target at https://<ip>:8200 and must match that
        peer's cert IP SAN.
      '';
    };

    tlsCertFile = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/vault/tls/server.crt";
      description = "Leaf cert + intermediate chain, seeded out-of-band (bin/gen_vault).";
    };

    tlsKeyFile = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/vault/tls/server.key";
      description = "Leaf private key, seeded out-of-band (0600 vault:vault).";
    };

    caFile = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/vault/tls/ca.crt";
      description = ''
        HSM CA chain (root+intermediate), seeded out-of-band. Used as
        leader_ca_cert_file for retry_join and as VAULT_CACERT for the local
        CLI.
      '';
    };

    sealSettingsPath = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/vault/seal.hcl";
      description = ''
        Path to the seeded file holding the `seal "transit" { ... }` stanza
        (including its token). Kept out of the Nix store and merged at
        runtime via services.vault.extraSettingsPaths. Vault will not start
        until this file exists.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = lib.elem cfg.ip cfg.peerIps;
        message = "vaultServer.ip (${cfg.ip}) must be one of vaultServer.peerIps.";
      }
    ];

    services.vault = {
      enable = true;

      # Bind wide; reachability is gated at the network/firewall layer
      # (today: internal 10.65.67.0/24 + the proxy front). The tailnet-only
      # phase later narrows the firewall to interfaces.tailscale0.
      address = "0.0.0.0:8200";

      tlsCertFile = cfg.tlsCertFile;
      tlsKeyFile = cfg.tlsKeyFile;

      storageBackend = "raft";
      storagePath = "/var/lib/vault";

      # cluster_address is a listener parameter; setting listenerExtraConfig
      # replaces the default block body, so restate tls_min_version too.
      listenerExtraConfig = ''
        tls_min_version = "tls12"
        cluster_address = "0.0.0.0:8201"
      '';

      # storage "raft" { ... } body: node_id + one retry_join per peer.
      storageConfig = ''
        node_id = "${cfg.nodeId}"
      ''
      + lib.concatMapStrings (peer: ''
        retry_join {
          leader_api_addr     = "https://${peer}:8200"
          leader_ca_cert_file = "${cfg.caFile}"
        }
      '') cfg.peerIps;

      # Top-level HCL.
      extraConfig = ''
        api_addr     = "https://${cfg.ip}:8200"
        cluster_addr = "https://${cfg.ip}:8201"
        # Recommended with integrated raft; VMs carry no swap (disko), so
        # disabling mlock does not risk key material hitting disk.
        disable_mlock = true
        ui = true
      '';

      # Transit auto-unseal stanza + token, seeded out-of-band.
      extraSettingsPaths = [ cfg.sealSettingsPath ];
    };

    # Raft data (and the seeded TLS/seal material under it) must survive the
    # per-boot ZFS rollback. Persist /var/lib/vault EXPLICITLY rather than
    # leaning on the /var/lib default, so the intent is reviewable.
    impermanence.extraPersistDirectories = [
      {
        path = /var/lib/vault;
        mode = "0700";
        owner = "vault";
        group = "vault";
      }
    ];

    # Internal listener + raft cluster port. TODO(tailnet phase): move these
    # to networking.firewall.interfaces.tailscale0.allowedTCPPorts and drop
    # the proxy/holepunch.
    networking.firewall.allowedTCPPorts = [
      8200
      8201
    ];

    # Don't race a not-yet-up network on boot; peers/transit must be
    # reachable for retry_join + auto-unseal.
    systemd.services.vault = {
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
    };

    # Local CLI against the loopback SAN (bin/gen_vault includes
    # IP:127.0.0.1), trusting the seeded HSM CA.
    environment.systemPackages = [ config.services.vault.package ];
    environment.variables = {
      VAULT_ADDR = "https://127.0.0.1:8200";
      VAULT_CACERT = cfg.caFile;
    };
  };
}
