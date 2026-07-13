# Declarative Salt master, mirroring the legacy ansible-provisioned
# master (automation/ansible/roles/setup_saltstack) so NixOS machines can
# join the (multi-)master pool.
#
# States/pillars come from gitfs against this repo, so masters carry no
# local state tree. Secret material (saltext-vault AppRole creds, the
# autosign grain key, and the shared master keypair) is rendered by
# vault-agent into /run/salt-master and pulled in via an absolute-path
# `default_include` glob, keeping it out of the nix store.
#
# All masters share one keypair (secret/infra/salt-master/pki) so minions
# with multiple masters configured accept any of them interchangeably.

{
  lib,
  pkgs,
  config,
  ...
}:

let
  cfg = config.saltMaster;
  runtimeDir = "/run/salt-master";
in
{
  imports = [
    ../vault-agent.nix
  ];

  options.saltMaster = {
    enable = lib.mkEnableOption "Salt master with gitfs states and saltext-vault";

    vaultKvBase = lib.mkOption {
      type = lib.types.str;
      default = "secret/data/infra/salt-master";
      description = "KV v2 base path holding the approle, pki and autosign secrets.";
    };

    vaultUrl = lib.mkOption {
      type = lib.types.str;
      default = "http://10.65.67.27:8201";
      description = "Vault server the salt master itself (saltext-vault) talks to.";
    };

    repo = lib.mkOption {
      type = lib.types.str;
      default = "https://github.com/general-programming/megarepo.git";
      description = "Git remote serving salt/state (gitfs) and salt/pillar (ext_pillar).";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = config.vaultAgent.enable;
        message = "saltMaster needs vaultAgent.enable to deliver the master secrets.";
      }
    ];

    # The NixOS salt module hardcodes pkgs.salt, so the gitfs and vault
    # extensions have to be injected via an overlay.
    nixpkgs.overlays = [
      (final: prev: {
        salt = prev.salt.override {
          extraInputs = [
            final.python3Packages.gitpython
            (final.callPackage ./saltext-vault.nix { })
          ];
        };
      })
    ];

    services.salt.master = {
      enable = true;
      configuration = {
        log_level = "info";

        # /var/lib persists under impermanence; /var/cache does not.
        cachedir = "/var/lib/salt/cache";

        # Secrets rendered by vault-agent; salt resolves absolute globs.
        default_include = "${runtimeDir}/master.d/*.conf";
        autosign_grains_dir = "${runtimeDir}/autosign_grains";

        # state config
        fileserver_backend = [ "git" ];
        gitfs_provider = "gitpython";
        gitfs_base = "main";
        gitfs_root = "salt/state";
        gitfs_remotes = [ cfg.repo ];

        # pillar config
        pillarenv_from_saltenv = true;
        top_file_merging_strategy = "same";
        ext_pillar = [
          {
            git = [
              {
                "main ${cfg.repo}" = [
                  { root = "salt/pillar"; }
                  { env = "base"; }
                ];
              }
            ];
          }
        ];

        # Minions fetch their vault config / secret_ids through the master.
        peer_run = {
          ".*" = [
            "vault.get_config"
            "vault.generate_secret_id"
          ];
        };
      };
    };

    vaultAgent.templates = {
      salt-master-vault = {
        destination = "${runtimeDir}/master.d/vault.conf";
        # Plain restart (not try-restart): the first render must also start
        # a unit held back by its ConditionPathExists.
        command = "systemctl restart salt-master.service";
        contents = ''
          {{- with secret "${cfg.vaultKvBase}/approle" }}
          vault:
            auth:
              method: approle
              approle_mount: salt-master
              role_id: {{ .Data.data.role_id }}
              secret_id: {{ .Data.data.secret_id }}
            server:
              url: ${cfg.vaultUrl}
            issue:
              type: approle
              approle:
                mount: salt-minions
            metadata:
              entity:
                minion-id: '{minion}'
                role: '{pillar[role]}'
            cache:
              backend: disk
          {{- end }}
        '';
      };

      salt-master-autosign = {
        destination = "${runtimeDir}/autosign_grains/vault_key";
        contents = ''
          {{- with secret "${cfg.vaultKvBase}/autosign" }}{{ .Data.data.key }}{{ end }}
        '';
      };

      salt-master-pem = {
        destination = "${runtimeDir}/master.pem";
        perms = "0400";
        command = "systemctl restart salt-master.service";
        contents = ''{{- with secret "${cfg.vaultKvBase}/pki" }}{{ .Data.data.master_pem }}{{ end }}'';
      };

      salt-master-pub = {
        destination = "${runtimeDir}/master.pub";
        perms = "0644";
        contents = ''{{- with secret "${cfg.vaultKvBase}/pki" }}{{ .Data.data.master_pub }}{{ end }}'';
      };
    };

    systemd.tmpfiles.rules = [
      "d ${runtimeDir} 0700 root root"
      "d ${runtimeDir}/master.d 0700 root root"
      "d ${runtimeDir}/autosign_grains 0700 root root"
    ];

    systemd.services.salt-master = {
      # GitPython shells out to git for gitfs/ext_pillar.
      path = [ pkgs.git ];
      wants = [ "network-online.target" ];
      after = [
        "network-online.target"
        "vault-agent-node.service"
      ];
      # Rendered by vault-agent; stay down until everything exists.
      unitConfig.ConditionPathExists = [
        "${runtimeDir}/master.d/vault.conf"
        "${runtimeDir}/autosign_grains/vault_key"
        "${runtimeDir}/master.pem"
        "${runtimeDir}/master.pub"
      ];
      # Seed the shared master keypair into the (persisted) pki dir so
      # every master presents the same identity to minions.
      preStart = ''
        install -d -m 0700 /var/lib/salt/pki/master
        install -m 0400 ${runtimeDir}/master.pem /var/lib/salt/pki/master/master.pem
        install -m 0644 ${runtimeDir}/master.pub /var/lib/salt/pki/master/master.pub
      '';
    };

    networking.firewall.allowedTCPPorts = [
      4505
      4506
    ];
  };
}
