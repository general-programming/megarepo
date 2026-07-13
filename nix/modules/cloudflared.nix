# Remotely-managed (token) Cloudflare tunnel. The upstream NixOS
# services.cloudflared module only supports locally-managed tunnels with a
# credentials file, so this runs `cloudflared tunnel run` with TUNNEL_TOKEN
# from a vault-agent rendered env file — no plaintext token in units or the
# nix store (the legacy Ubuntu box had it baked into the unit file).

{
  lib,
  pkgs,
  config,
  ...
}:

let
  cfg = config.gpCloudflared;
in
{
  imports = [
    ./vault-agent.nix
  ];

  options.gpCloudflared = {
    enable = lib.mkEnableOption "token-based Cloudflare tunnel";

    vaultKvPath = lib.mkOption {
      type = lib.types.str;
      default = "secret/data/infra/cloudflared/${config.networking.hostName}";
      description = "KV v2 path holding the tunnel token in a `token` field.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = config.vaultAgent.enable;
        message = "gpCloudflared needs vaultAgent.enable to deliver the tunnel token.";
      }
    ];

    vaultAgent.templates.cloudflared = {
      destination = "/run/vault-agent/cloudflared.env";
      command = "systemctl restart cloudflared-tunnel.service";
      contents = ''
        {{- with secret "${cfg.vaultKvPath}" }}
        TUNNEL_TOKEN={{ .Data.data.token }}
        {{- end }}
      '';
    };

    systemd.services.cloudflared-tunnel = {
      description = "Cloudflare tunnel (remotely managed)";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [
        "network-online.target"
        "vault-agent-node.service"
      ];
      # Rendered by vault-agent; the template command starts us on first render.
      unitConfig.ConditionPathExists = "/run/vault-agent/cloudflared.env";
      serviceConfig = {
        ExecStart = "${pkgs.cloudflared}/bin/cloudflared --no-autoupdate tunnel run";
        EnvironmentFile = "/run/vault-agent/cloudflared.env";
        DynamicUser = true;
        Restart = "on-failure";
        RestartSec = "5s";
      };
    };
  };
}
