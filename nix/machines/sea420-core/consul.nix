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
}
