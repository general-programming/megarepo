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
            bind_addr = "[::]";
            retry_join = [
                "2602:fa6d:10:ffff::101"
                "2602:fa6d:10:ffff::102"
                "2602:fa6d:10:ffff::103"
            ];
            alt_domain = "consul.generalprogramming.org";
        };
    };
}