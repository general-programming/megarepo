# Kea DHCPv4 + DHCPv6 with NetBox-driven host reservations (Stage 2 of the
# ISC-DHCP/dnsmasq-DHCP retirement). Reservations are global and keyed on
# hw-address for both families — Talos clients mint a fresh DUID-LLT every
# lease cycle, so DUID-keyed v6 reservations can never be stable. The hourly
# refresh renders hosts{4,6}.json from NetBox and restarts Kea on change;
# leases persist in memfile across restarts. The run_script hook posts
# committed leases to the Discord webhook worker (parity with the dnsmasq
# dhcp-script and the legacy isc-dhcpd on-commit hook).

{
  lib,
  pkgs,
  config,
  ...
}:

let
  cfg = config.kea;

  hostsDir = "/var/lib/kea/netbox";
  hosts4 = "${hostsDir}/hosts4.json";
  hosts6 = "${hostsDir}/hosts6.json";

  barf = pkgs.callPackage ../../pkgs/barf.nix { };

  webhookScript = pkgs.writers.writePython3Bin "kea-dhcp-webhook" { } (
    builtins.readFile ./webhook_adapter.py
  );

  runScriptHook = "${pkgs.kea}/lib/kea/hooks/libdhcp_run_script.so";

  hooksLibraries = lib.optional cfg.webhook.enable {
    library = runScriptHook;
    parameters = {
      name = "${webhookScript}/bin/kea-dhcp-webhook";
      sync = false;
    };
  };

  # The reservations arrays live outside the store so the hourly refresh can
  # rewrite them; Kea's <?include?> can't be expressed in a Nix attrset, so
  # render settings to JSON and substitute a placeholder.
  withInclude = json: placeholder: path:
    builtins.replaceStrings [ "\"${placeholder}\"" ] [ "<?include \"${path}\"?>" ] json;

  dhcp4Config = pkgs.writeText "kea-dhcp4.conf" (
    withInclude (builtins.toJSON {
      Dhcp4 = {
        interfaces-config.interfaces = cfg.interfaces;
        authoritative = true;
        lease-database = {
          type = "memfile";
          persist = true;
          name = "/var/lib/kea/dhcp4.leases";
        };
        reservations-global = true;
        reservations-in-subnet = false;
        option-data = cfg.dhcp4.optionData;
        client-classes = cfg.dhcp4.clientClasses;
        subnet4 = cfg.dhcp4.subnets;
        shared-networks = cfg.dhcp4.sharedNetworks;
        reservations = "@NETBOX_HOSTS4@";
        hooks-libraries = hooksLibraries;
      };
    }) "@NETBOX_HOSTS4@" hosts4
  );

  dhcp6Config = pkgs.writeText "kea-dhcp6.conf" (
    withInclude (builtins.toJSON {
      Dhcp6 = {
        interfaces-config.interfaces = cfg.interfaces;
        lease-database = {
          type = "memfile";
          persist = true;
          name = "/var/lib/kea/dhcp6.leases";
        };
        # MAC-keyed reservations need Kea to learn the client MAC; on-link
        # clients expose it via the DUID (LLT/LL embed it) or their
        # link-local source address.
        host-reservation-identifiers = [ "hw-address" "duid" ];
        mac-sources = [ "duid" "ipv6-link-local" ];
        reservations-global = true;
        reservations-in-subnet = false;
        option-data = cfg.dhcp6.optionData;
        subnet6 = cfg.dhcp6.subnets;
        reservations = "@NETBOX_HOSTS6@";
        hooks-libraries = hooksLibraries;
      };
    }) "@NETBOX_HOSTS6@" hosts6
  );

  # The kea daemons run as a DynamicUser; the refresh service runs as root.
  # Keep the reservation files world-readable (MAC/IP inventory, not
  # secret) so both sides can always read them. Must run as root (systemd
  # "+" ExecStartPre prefix): the files are root-owned, so an unprivileged
  # preStart can neither chmod nor replace them.
  seedScript = pkgs.writeShellScript "kea-seed-netbox-hosts" ''
    mkdir -p ${hostsDir}
    chmod 0755 ${hostsDir}
    [ -s ${hosts4} ] || echo "[]" > ${hosts4}
    [ -s ${hosts6} ] || echo "[]" > ${hosts6}
    chmod 0644 ${hosts4} ${hosts6}
  '';
in
{
  imports = [
    ../vault-agent.nix
  ];

  options.kea = {
    enable = lib.mkEnableOption "Kea DHCP with NetBox reservations";

    interfaces = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      description = "Interfaces Kea listens on (primary kernel names).";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Open UDP 67 (and 547 when dhcp6 is enabled).";
    };

    dhcp4 = {
      subnets = lib.mkOption {
        type = lib.types.listOf lib.types.attrs;
        description = "Raw Kea subnet4 entries (id, subnet, pools, option-data, ...).";
      };

      optionData = lib.mkOption {
        type = lib.types.listOf lib.types.attrs;
        default = [ ];
        description = "Global Dhcp4 option-data entries.";
      };

      clientClasses = lib.mkOption {
        type = lib.types.listOf lib.types.attrs;
        default = [ ];
        description = ''
          Raw Kea client-classes entries (name, test, boot-file-name, ...).
          For boot-file-name, the first matching class in list order wins.
        '';
      };

      sharedNetworks = lib.mkOption {
        type = lib.types.listOf lib.types.attrs;
        default = [ ];
        description = ''
          Raw Kea shared-networks entries (name, interface, subnet4, ...).
          Subnet ids must be unique across these and dhcp4.subnets.
        '';
      };
    };

    dhcp6 = {
      enable = lib.mkEnableOption "Kea DHCPv6";

      subnets = lib.mkOption {
        type = lib.types.listOf lib.types.attrs;
        default = [ ];
        description = "Raw Kea subnet6 entries.";
      };

      optionData = lib.mkOption {
        type = lib.types.listOf lib.types.attrs;
        default = [ ];
        description = "Global Dhcp6 option-data entries.";
      };
    };

    webhook = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "POST committed leases to the Discord webhook worker.";
      };

      vaultKvPath = lib.mkOption {
        type = lib.types.str;
        default = "secret/data/infra/dhcp-webhook";
        description = "KV v2 path holding the worker URL in a `url` field.";
      };
    };

    refresh = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Hourly reservation refresh from NetBox.";
      };

      netboxUrl = lib.mkOption {
        type = lib.types.str;
        default = "https://netbox.generalprogramming.org/graphql/";
        description = "NetBox GraphQL endpoint.";
      };

      vaultKvPath = lib.mkOption {
        type = lib.types.str;
        default = "secret/data/infra/netbox";
        description = "KV v2 path holding the NetBox API key in an `api_key` field.";
      };
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = !(cfg.webhook.enable || cfg.refresh.enable) || config.vaultAgent.enable;
        message = "kea webhook/refresh need vaultAgent.enable for their secrets.";
      }
    ];

    services.kea.dhcp4 = {
      enable = true;
      configFile = dhcp4Config;
    };

    services.kea.dhcp6 = lib.mkIf cfg.dhcp6.enable {
      enable = true;
      configFile = dhcp6Config;
    };

    # Seed empty reservation arrays so Kea can start before the first
    # NetBox refresh has run. KEA_HOOK_SCRIPTS_PATH: kea >= 2.7.9 only
    # runs hook scripts from a supported directory (compared by exact
    # dirname), so point it at the webhook script's bin dir.
    systemd.services.kea-dhcp4-server = {
      serviceConfig.ExecStartPre = [ "+${seedScript}" ];
      environment = lib.mkIf cfg.webhook.enable {
        KEA_HOOK_SCRIPTS_PATH = "${webhookScript}/bin";
      };
    };
    systemd.services.kea-dhcp6-server = lib.mkIf cfg.dhcp6.enable {
      serviceConfig.ExecStartPre = [ "+${seedScript}" ];
      environment = lib.mkIf cfg.webhook.enable {
        KEA_HOOK_SCRIPTS_PATH = "${webhookScript}/bin";
      };
    };

    systemd.services.kea-netbox-refresh = lib.mkIf cfg.refresh.enable {
      description = "Refresh Kea host reservations from NetBox";
      # Rendered by vault-agent; skip quietly until it exists.
      unitConfig.ConditionPathExists = "/run/vault-agent/netbox-kea.env";
      environment.NETBOX_URL = cfg.refresh.netboxUrl;
      serviceConfig = {
        Type = "oneshot";
        EnvironmentFile = "/run/vault-agent/netbox-kea.env";
      };
      path = [ pkgs.diffutils ];
      script = ''
        mkdir -p ${hostsDir}
        ${lib.getExe barf} generate dhcp --format kea \
          --output4 ${hosts4}.new --output6 ${hosts6}.new

        changed=0
        for f in ${hosts4} ${hosts6}; do
          if ! cmp -s "$f.new" "$f"; then
            chmod 0644 "$f.new"
            mv "$f.new" "$f"
            changed=1
          else
            rm -f "$f.new"
          fi
        done
        chmod 0755 ${hostsDir}

        if [ "$changed" = 1 ]; then
          systemctl try-restart kea-dhcp4-server.service ${
            lib.optionalString cfg.dhcp6.enable "kea-dhcp6-server.service"
          }
        fi
      '';
    };

    systemd.timers.kea-netbox-refresh = lib.mkIf cfg.refresh.enable {
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "5m";
        OnUnitActiveSec = "1h";
        RandomizedDelaySec = "5m";
      };
    };

    vaultAgent.templates = {
      netbox-kea = lib.mkIf cfg.refresh.enable {
        destination = "/run/vault-agent/netbox-kea.env";
        contents = ''
          {{- with secret "${cfg.refresh.vaultKvPath}" }}
          NETBOX_API_KEY={{ .Data.data.api_key }}
          {{- end }}
        '';
      };

      kea-dhcp-webhook = lib.mkIf cfg.webhook.enable {
        destination = "/run/vault-agent/kea-dhcp-webhook.env";
        # The hook runs as kea's DynamicUser, which has no static group to
        # chgrp to at render time; the webhook URL is low-sensitivity, so
        # world-readable is the pragmatic option.
        perms = "0644";
        contents = ''
          {{- with secret "${cfg.webhook.vaultKvPath}" }}
          WEBHOOK_URL={{ .Data.data.url }}
          {{- end }}
        '';
      };
    };

    networking.firewall.allowedUDPPorts =
      lib.mkIf cfg.openFirewall ([ 67 ] ++ lib.optional cfg.dhcp6.enable 547);
  };
}
