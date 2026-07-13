# Client-side configuration for using `sea1-nix-builder` (a Proxmox LXC, see
# nix/machines/sea1-nix-builder/configuration.nix) as a remote Nix builder.
#
# Disabled until the container is actually provisioned (see
# scripts/provision-builder-lxc.py) and its address/host key are known.

{
  lib,
  config,
  ...
}:

let
  cfg = config.nixBuilder;
in
{
  imports = [ ./vault-agent.nix ];

  options.nixBuilder = {
    enable = lib.mkEnableOption "using the shared `builder` host as a remote Nix builder";

    hostName = lib.mkOption {
      type = lib.types.str;
      default = "builder.generalprogramming.org";
      description = "Address of the builder container.";
    };

    publicHostKey = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = ''
        Base64 SSH host public key of the builder container (`base64 -w0
        /etc/ssh/ssh_host_ed25519_key.pub` on the container), so dispatching
        hosts don't need a `known_hosts` entry.
      '';
    };

    maxJobs = lib.mkOption {
      type = lib.types.int;
      default = 4;
      description = "Maximum concurrent jobs to dispatch to the builder.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.publicHostKey != null;
        message = "nixBuilder.publicHostKey must be set when nixBuilder.enable is true.";
      }
      {
        assertion = config.vaultAgent.enable;
        message = "nixBuilder needs vaultAgent.enable to deliver the builder SSH private key.";
      }
    ];

    vaultAgent.templates.nixBuilderKey = {
      contents = ''
        {{- with secret "secret/app/nix-builder-ssh" }}
        {{ .Data.data.private_key }}
        {{- end }}
      '';
      destination = "/run/vault-agent/nix-builder-key";
      perms = "0400";
    };

    nix.distributedBuilds = true;
    nix.buildMachines = [
      {
        hostName = cfg.hostName;
        sshUser = "builder";
        sshKey = "/run/vault-agent/nix-builder-key";
        system = "x86_64-linux";
        maxJobs = cfg.maxJobs;
        publicHostKey = cfg.publicHostKey;
      }
    ];
  };
}
