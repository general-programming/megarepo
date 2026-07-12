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
            bind_addr = "{{ GetPrivateIP }}";
            retry_join = [
                "10.65.67.47"
                "10.65.67.48"
                "10.65.67.49"
            ];
        };
    };
}