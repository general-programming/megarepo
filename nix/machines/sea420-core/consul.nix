# Single-server consul for the sea420 dc, plus the node_exporter service
# registration the Salt config carried.
{ ... }:
{
  services.consul = {
    enable = true;
    extraConfig = {
      server = true;
      bootstrap_expect = 1;
      ui = false;
      enable_local_script_checks = true;
      datacenter = "sea420";
      bind_addr = "10.3.0.3";
      retry_join = [ "consul.service.sea420.consul" ];
      alt_domain = "consul.generalprogramming.org";

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

  # This is the only consul server in dc sea420, so the site's clients
  # (sea420-hv-2, wob-app-cream) connect inbound. The Ubuntu box filtered
  # nothing; NixOS defaults to deny, so open server RPC + LAN serf or the
  # cluster loses every client. 8302/WAN serf stays closed: sea420 is not
  # WAN-federated (`consul members -wan` lists only itself) -- open it here
  # if that ever changes. HTTP/DNS (8500/8600) bind 127.0.0.1 only.
  networking.firewall = {
    allowedTCPPorts = [
      8300
      8301
    ];
    allowedUDPPorts = [ 8301 ];
  };
}
