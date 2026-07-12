{
  lib,
  vars',
  config,
  inputs,
  ...
}:

{
  imports = [
    inputs.comin.nixosModules.comin
  ];

  options.gitops = {
    enable = lib.mkEnableOption "GitOps from general-programming/nixos";

    ref = lib.mkOption {
      type = lib.types.str;
      default = "main";
      description = "Git reference (branch, tag, commit) to use for GitOps.";
    };

    repo = lib.mkOption {
      type = lib.types.str;
      readOnly = true;
      description = "Git repository URL to use for GitOps.";
    };

    subdir = lib.mkOption {
      type = lib.types.str;
      readOnly = true;
      description = "Subdirectory in the Git repository to use for GitOps.";
    };

    interval = lib.mkOption {
      type = lib.types.int;
      default = 600;
      description = "Interval in seconds to check for updates.";
    };
  };

  config = {
    # assertions = lib.mkIf config.gitops.enable [
    #   {
    #     assertion = vars' ? "machineID";
    #     message = "machineID must be set in vars.nix for gitops to work.";
    #   }
    # ];

    gitops = {
      repo = "https://github.com/general-programming/nixos.git";
      ref = "main";
    };

    services.comin = {
      enable = config.gitops.enable;
      remotes = [
        {
          name = "origin";
          url = config.gitops.repo;
          poller.period = config.gitops.interval;
          branches.main.name = config.gitops.ref;
        }
      ];
      machineId = vars'.machineID;
      # machineId = null;
      repositorySubdir = config.gitops.subdir;
      repositoryType = "flake";
      exporter =
        if vars'.ports.comin-exporter != null then
          {
            listen_address = "127.0.0.1";
            port = vars'.ports.comin-exporter;
            openFirewall = false;
          }
        else
          { };
    };

    # Allow probing exporter via Tailscale.
    networking.firewall.interfaces.tailscale0.allowedTCPPorts =
      lib.mkIf config.services.tailscale.enable
        (
          if vars'.ports.comin-exporter != null then
            [
              vars'.ports.comin-exporter
            ]
          else
            [ ]
        );
  };
}
