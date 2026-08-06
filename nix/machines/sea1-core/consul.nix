{
  modulesPath,
  lib,
  pkgs,
  ...
}:
{
    services.consul = {
        enable = true;
        extraConfig = {
            server = false;
            ui = false;
            enable_local_script_checks = true;
            datacenter = "sea1";
            # Was "[::]". The sea1 servers used to be the Proxmox hypervisors,
            # which bound a concrete IPv6 address; they are now a StatefulSet on
            # the Talos nodes whose config binds 0.0.0.0, so the whole DC gossips
            # over IPv4 on 10.3.2.0/23. A literal beats a go-sockaddr template
            # here -- this host also holds a 100.64/10 tailscale address, and the
            # agent refuses to start if the template resolves to more than one.
            bind_addr = "10.3.2.6";
            # The sea1 k8s nodes. The consul servers publish 8300/8301/8302 as
            # hostPorts, so these node addresses are the server addresses.
            retry_join = [
                "10.3.2.10"
                "10.3.2.11"
                "10.3.2.12"
            ];
            alt_domain = "consul.generalprogramming.org";

            # node_exporter lived in the sea1 catalog only because salt
            # registered it on the three hypervisors. Those are gone, so without
            # this the vmagent consul SD in
            # argocd/apps/infra/victoriametrics/sea1/sea1_scrape.yaml discovers
            # an empty target list and silently stops scraping. base.nix already
            # runs the exporter on :9100.
            services = [
                {
                    name = "node_exporter";
                    id = "node_exporter";
                    port = 9100;
                    tags = [
                        "node_exporter"
                        "prometheus"
                    ];
                    checks = [
                        {
                            name = "node_exporter on port 9100";
                            interval = "10s";
                            http = "http://localhost:9100";
                            timeout = "5s";
                        }
                    ];
                }
            ];
        };
    };

    # NixOS defaults to deny and nothing opened these, so the servers could not
    # probe this agent inbound -- serf needs both directions or the node flaps
    # between alive and failed. Client only: no 8300 (server RPC) and no 8302
    # (WAN serf, sea1 is not federated). HTTP/DNS stay on 127.0.0.1.
    networking.firewall = {
        allowedTCPPorts = [ 8301 ];
        allowedUDPPorts = [ 8301 ];
    };
}
