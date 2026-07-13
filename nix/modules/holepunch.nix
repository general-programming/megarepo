# Vault-proxy port knock ("holepunch"): POST {"key": ...} to the punch
# endpoint so this host's source IP stays allowed through to the Vault
# proxy. Nix port of the ansible setup_cluster_network role's
# vault_holepunch cron (and the hand-managed crontab on legacy sea1-core).

{
  lib,
  pkgs,
  config,
  ...
}:

let
  cfg = config.holepunch;
in
{
  imports = [
    ./vault-agent.nix
  ];

  options.holepunch = {
    enable = lib.mkEnableOption "periodic vault-proxy port knock";

    url = lib.mkOption {
      type = lib.types.str;
      default = "http://79.110.170.57:8000/punch";
      description = "Punch endpoint the knock is POSTed to.";
    };

    interval = lib.mkOption {
      type = lib.types.str;
      default = "hourly";
      description = "systemd OnCalendar expression for the knock timer.";
    };

    vaultKvPath = lib.mkOption {
      type = lib.types.str;
      default = "secret/data/infra/holepunch";
      description = "KV v2 path holding the knock key in a `key` field.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = config.vaultAgent.enable;
        message = "holepunch needs vaultAgent.enable to deliver the knock key.";
      }
    ];

    vaultAgent.templates.holepunch = {
      destination = "/run/vault-agent/holepunch.env";
      contents = ''
        {{- with secret "${cfg.vaultKvPath}" }}
        HOLEPUNCH_KEY={{ .Data.data.key }}
        {{- end }}
      '';
    };

    systemd.services.holepunch = {
      description = "Vault-proxy port knock";
      wants = [ "network-online.target" ];
      after = [
        "network-online.target"
        "vault-agent-node.service"
      ];
      # Rendered by vault-agent; skip quietly until it exists.
      unitConfig.ConditionPathExists = "/run/vault-agent/holepunch.env";
      serviceConfig = {
        Type = "oneshot";
        EnvironmentFile = "/run/vault-agent/holepunch.env";
      };
      script = ''
        ${pkgs.curl}/bin/curl -sS -m 10 -X POST ${lib.escapeShellArg cfg.url} \
          -H 'Content-Type: application/json' \
          --data "{\"key\": \"$HOLEPUNCH_KEY\"}"
      '';
    };

    systemd.timers.holepunch = {
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnCalendar = cfg.interval;
        # Knock shortly after boot too, not just on the calendar.
        OnBootSec = "2m";
        RandomizedDelaySec = "2m";
        Persistent = true;
      };
    };
  };
}
