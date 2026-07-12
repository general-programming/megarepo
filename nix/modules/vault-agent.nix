# Vault Agent with AppRole auth, mirroring the ansible
# setup_cluster_vault_agent role used on the Nomad cluster nodes.
#
# The agent authenticates with role_id/secret_id files from
# vaultAgent.credentialsDir (seeded by `just provision` / `just rekey`),
# keeps a token sink in /run/vault-agent, and exposes a local caching
# proxy so services can talk to Vault without their own token.
#
# credentialsDir lives under /var/lib, which the impermanence module
# bind-mounts from /persist by default, so the AppRole pair survives
# reboots on impermanent machines too. Rendered templates should target
# /run so secrets never touch persistent disk.

{
  lib,
  config,
  ...
}:

let
  cfg = config.vaultAgent;
in
{
  options.vaultAgent = {
    enable = lib.mkEnableOption "Vault Agent with AppRole auth";

    address = lib.mkOption {
      type = lib.types.str;
      default = "http://10.65.67.27:8201";
      description = "Address of the Vault server.";
    };

    credentialsDir = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/vault-agent";
      description = "Directory holding the AppRole role_id/secret_id files.";
    };

    listenerAddress = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:8100";
      description = "Address of the local caching proxy listener.";
    };

    templates = lib.mkOption {
      type = lib.types.attrsOf (
        lib.types.submodule {
          options = {
            contents = lib.mkOption {
              type = lib.types.str;
              description = "consul-template contents rendering the secret.";
            };
            destination = lib.mkOption {
              type = lib.types.str;
              description = "Path the rendered secret is written to.";
            };
            perms = lib.mkOption {
              type = lib.types.str;
              default = "0600";
              description = "File mode of the rendered secret.";
            };
            command = lib.mkOption {
              type = lib.types.nullOr lib.types.str;
              default = null;
              description = "Command to run after the template is rendered.";
            };
          };
        }
      );
      default = { };
      description = "Secrets to render to disk, keyed by a friendly name.";
    };
  };

  config = lib.mkIf cfg.enable {
    services.vault-agent.instances.node.settings = {
      vault.address = cfg.address;

      auto_auth = {
        method = [
          {
            type = "approle";
            config = {
              role_id_file_path = "${cfg.credentialsDir}/approle_id";
              secret_id_file_path = "${cfg.credentialsDir}/approle_secret";
              remove_secret_id_file_after_reading = false;
            };
          }
        ];

        sink = [
          {
            type = "file";
            config.path = "/run/vault-agent/token";
          }
        ];
      };

      cache.use_auto_auth_token = true;

      listener = [
        {
          type = "tcp";
          address = cfg.listenerAddress;
          tls_disable = true;
        }
      ];

      template = lib.mapAttrsToList (
        _: t:
        {
          inherit (t) contents destination perms;
        }
        // lib.optionalAttrs (t.command != null) { command = t.command; }
      ) cfg.templates;
    };

    systemd.tmpfiles.rules = [
      "d ${cfg.credentialsDir} 0700 root root"
    ];

    # Don't flap while the network (or Vault itself) is still coming up.
    systemd.services.vault-agent-node = {
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      startLimitIntervalSec = lib.mkForce 0;
      serviceConfig.RestartSec = "5s";
    };
  };
}
