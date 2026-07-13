{
  lib,
  pkgs,
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
    enable = lib.mkEnableOption "GitOps from general-programming/megarepo";

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
      repo = "https://github.com/general-programming/megarepo.git";
      ref = "main";
      subdir = "nix";
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

    # comin's bare mirror clone never gets a valid HEAD (its refspec only
    # updates refs/remotes/origin/*, leaving HEAD on an unborn master), so
    # lix's fetchGit resolves the wrong ref and poisons its git fetcher
    # cache. Point HEAD at the tracked branch and drop the poisoned cache
    # when it was wrong. TODO: fix upstream in comin.
    systemd.services.comin.preStart = lib.mkIf config.gitops.enable ''
      repo=/var/lib/comin/repository
      want=refs/remotes/origin/${config.gitops.ref}
      if [ -d "$repo" ] && [ "$(${pkgs.git}/bin/git -C "$repo" symbolic-ref HEAD)" != "$want" ]; then
        ${pkgs.git}/bin/git -C "$repo" symbolic-ref HEAD "$want"
        rm -rf /root/.cache/nix/gitv3
      fi
    '';

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
