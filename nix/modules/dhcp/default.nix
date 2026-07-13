# dnsmasq DHCP on top of the dns module's dnsmasq: per-host ranges and
# options, plus the shared Discord lease webhook (dnsmasq dhcp-script port
# of the legacy isc-dhcpd on-commit hook). Static NetBox reservations
# (dhcp-host=) already come from the dns module's NetBox refresh.

{
  lib,
  pkgs,
  config,
  ...
}:

let
  cfg = config.dhcp;

  webhookScript = pkgs.writers.writePython3Bin "dhcp-webhook" { } (
    builtins.readFile ./webhook.py
  );
in
{
  imports = [
    ../vault-agent.nix
  ];

  options.dhcp = {
    enable = lib.mkEnableOption "dnsmasq DHCP";

    ranges = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      description = "dnsmasq dhcp-range values (one per line), set per host.";
    };

    extraOptions = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "dnsmasq dhcp-option values (router, dns, mtu, ...).";
    };

    authoritative = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Answer as the authoritative DHCP server on the subnet.";
    };

    webhook = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "POST new/renewed leases to the Discord webhook worker.";
      };

      vaultKvPath = lib.mkOption {
        type = lib.types.str;
        default = "secret/data/infra/dhcp-webhook";
        description = "KV v2 path holding the worker URL in a `url` field.";
      };
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = config.services.dnsmasq.enable;
        message = "dhcp rides on the dns module's dnsmasq; import/enable it first.";
      }
      {
        assertion = !cfg.webhook.enable || config.vaultAgent.enable;
        message = "dhcp.webhook needs vaultAgent.enable to deliver the worker URL.";
      }
    ];

    services.dnsmasq.settings = {
      dhcp-range = cfg.ranges;
      dhcp-option = cfg.extraOptions;
      dhcp-authoritative = cfg.authoritative;
      dhcp-script = lib.mkIf cfg.webhook.enable "${webhookScript}/bin/dhcp-webhook";
    };

    vaultAgent.templates.dhcp-webhook = lib.mkIf cfg.webhook.enable {
      destination = "/run/vault-agent/dhcp-webhook.env";
      # dnsmasq runs the dhcp-script as the dnsmasq user; the template
      # perms option can't set group, so fix ownership after each render.
      perms = "0640";
      command = "chgrp dnsmasq /run/vault-agent/dhcp-webhook.env";
      contents = ''
        {{- with secret "${cfg.webhook.vaultKvPath}" }}
        WEBHOOK_URL={{ .Data.data.url }}
        {{- end }}
      '';
    };

    networking.firewall.allowedUDPPorts = [
      67 # DHCPv4
      547 # DHCPv6
    ];
  };
}
