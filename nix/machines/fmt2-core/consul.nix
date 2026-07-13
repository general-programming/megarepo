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
            datacenter = "fmt2";
            alt_domain = "consul.generalprogramming.org";
            bind_addr = "{{ GetPrivateIP }}";
            retry_join = [
                "10.65.67.47"
                "10.65.67.48"
                "10.65.67.49"
            ];
        };
    };
}