{
  modulesPath,
  lib,
  pkgs,
  config,
  ...
}:
let
    cfg = config.dns.refresh;

    netbox_gen = builtins.fromJSON (builtins.readFile ./netbox.json);
    netbox_addresses = builtins.map ({fqdn, ip, ...}: "/${fqdn}/${ip}") netbox_gen.addresses;
    netbox_ptrs = builtins.map ({reverse_arpa, fqdn, ...}: "${reverse_arpa},${fqdn}") netbox_gen.ptr_records;

    # Live NetBox-generated config; owned by the refresh timer, read via
    # conf-dir so it never fights the module-managed conf-file list.
    generatedDir = "/var/lib/dnsmasq/netbox.d";
    generatedConf = "${generatedDir}/netbox.conf";

    # Boot-time seed from the checked-in netbox.json so dnsmasq can start
    # before (or without) the first live refresh.
    seedConf = pkgs.writeText "netbox-seed.conf" (lib.concatStringsSep "\n" (
        [ "# Seeded from netbox.json; replaced hourly by netbox-dns-refresh." ]
        ++ (builtins.map (address: "address=${address}") netbox_addresses)
        ++ (builtins.map (ptr: "ptr-record=${ptr}") netbox_ptrs)
    ) + "\n");

    refreshScript = pkgs.writeScriptBin "netbox-dnsmasq" ''
        #!${pkgs.python3}/bin/python3
        ${builtins.readFile ./refresh_dns.py}
    '';
in
{
    imports = [
        ../vault-agent.nix
    ];

    options.dns.refresh = {
        enable = lib.mkEnableOption "periodic dnsmasq DNS/DHCP refresh from NetBox";

        vaultKvPath = lib.mkOption {
            type = lib.types.str;
            default = "secret/data/infra/netbox";
            description = "KV v2 path holding the NetBox API key in an `api_key` field.";
        };

        netboxUrl = lib.mkOption {
            type = lib.types.str;
            default = "https://netbox.generalprogramming.org/graphql/";
            description = "NetBox GraphQL endpoint.";
        };

        interval = lib.mkOption {
            type = lib.types.str;
            default = "hourly";
            description = "systemd OnCalendar expression for the refresh timer.";
        };
    };

    config = lib.mkMerge [
        {
            networking.nameservers = ["127.0.0.1"];
            services.resolved.enable = false;
            services.dnsmasq = {
                enable = true;
                alwaysKeepRunning = true;
                settings = {
                    server = [
                        "/ipa.generalprogramming.org/10.3.0.4"
                        "/ipa.generalprogramming.org/10.65.67.14"
                        "/devhack.net/10.213.0.50"
                        "/consul/127.0.0.1#8600"
                        "1.1.1.1"
                        "1.0.0.1"
                    ];
                    # With refresh enabled these move into the conf-dir file;
                    # baked entries would otherwise shadow updated ones.
                    address = lib.mkIf (!cfg.enable) netbox_addresses;
                    ptr-record = lib.mkIf (!cfg.enable) netbox_ptrs;
                };
            };

            # Expand the firewall too
            networking.firewall.allowedTCPPorts = [
                53
            ];

            networking.firewall.allowedUDPPorts = [
                53
            ];
        }

        (lib.mkIf cfg.enable {
            assertions = [
                {
                    assertion = config.vaultAgent.enable;
                    message = "dns.refresh needs vaultAgent.enable to deliver the NetBox API key.";
                }
            ];

            vaultAgent.templates.netbox = {
                contents = ''
                    {{- with secret "${cfg.vaultKvPath}" }}
                    NETBOX_API_KEY={{ .Data.data.api_key }}
                    {{- end }}
                '';
                destination = "/run/vault-agent/netbox.env";
            };

            services.dnsmasq.settings.conf-dir = generatedDir;

            systemd.services.dnsmasq.preStart = lib.mkBefore ''
                if [ ! -e ${generatedConf} ]; then
                    install -D -m 0644 ${seedConf} ${generatedConf}
                fi
            '';

            systemd.services.netbox-dns-refresh = {
                description = "Refresh dnsmasq DNS/DHCP entries from NetBox";
                wants = [ "network-online.target" ];
                after = [
                    "network-online.target"
                    "vault-agent-node.service"
                ];
                # Rendered by vault-agent; skip quietly until it exists.
                unitConfig.ConditionPathExists = "/run/vault-agent/netbox.env";
                environment.NETBOX_URL = cfg.netboxUrl;
                serviceConfig = {
                    Type = "oneshot";
                    EnvironmentFile = "/run/vault-agent/netbox.env";
                };
                path = [
                    pkgs.dnsmasq
                    pkgs.systemd
                ];
                script = ''
                    new=$(mktemp)
                    trap 'rm -f "$new"' EXIT

                    ${refreshScript}/bin/netbox-dnsmasq --output "$new"
                    dnsmasq --test --conf-file="$new"

                    if ! cmp -s "$new" ${generatedConf}; then
                        install -D -m 0644 "$new" ${generatedConf}
                        systemctl try-restart dnsmasq.service
                        echo "NetBox dnsmasq config updated."
                    else
                        echo "NetBox dnsmasq config unchanged."
                    fi
                '';
            };

            systemd.timers.netbox-dns-refresh = {
                wantedBy = [ "timers.target" ];
                timerConfig = {
                    OnCalendar = cfg.interval;
                    RandomizedDelaySec = "5m";
                    Persistent = true;
                };
            };
        })
    ];
}
