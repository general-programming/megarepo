# Tailscale joined with a pre-auth key from Vault. The module only wires
# secret delivery; per-host routing policy (exit node, advertised routes,
# useRoutingFeatures) belongs in the machine config.
#
# The auth key is only consumed while the node is logged out — tailscaled
# state persists in /var/lib/tailscale (kept by impermanence), so an
# expired or already-used key is harmless after the first join.

{
  lib,
  config,
  ...
}:

let
  cfg = config.gpTailscale;
  keyFile = "/run/vault-agent/tailscale-authkey";
in
{
  imports = [
    ./vault-agent.nix
  ];

  options.gpTailscale = {
    enable = lib.mkEnableOption "Tailscale with a Vault-delivered pre-auth key";

    vaultKvPath = lib.mkOption {
      type = lib.types.str;
      default = "secret/data/infra/tailscale";
      description = "KV v2 path holding the pre-auth key in an `authkey` field.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = config.vaultAgent.enable;
        message = "gpTailscale needs vaultAgent.enable to deliver the auth key.";
      }
    ];

    vaultAgent.templates.tailscale-authkey = {
      destination = keyFile;
      # Plain restart (not try-restart): the first render must also start
      # a unit that was condition-skipped at boot.
      command = "systemctl restart tailscaled-autoconnect.service";
      contents = ''
        {{- with secret "${cfg.vaultKvPath}" }}{{ .Data.data.authkey }}{{ end }}
      '';
    };

    services.tailscale = {
      enable = true;
      authKeyFile = keyFile;
    };

    systemd.services.tailscaled-autoconnect = {
      after = [ "vault-agent-node.service" ];
      # Skip quietly until vault-agent has rendered the key; once the node
      # is logged in the unit no-ops anyway.
      unitConfig.ConditionPathExists = keyFile;
    };
  };
}
