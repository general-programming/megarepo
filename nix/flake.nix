{
  inputs = {
      nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

      lix = {
        url = "git+https://git@git.lix.systems/lix-project/lix";
      };

      lix-module = {
        url = "git+https://git.lix.systems/lix-project/nixos-module";
        inputs.lix.follows = "lix";
        inputs.nixpkgs.follows = "nixpkgs";
      };

      disko = {
        url = "github:nix-community/disko";
        inputs.nixpkgs.follows = "nixpkgs";
      };

      comin.url = "github:nlewo/comin";
      comin.inputs.nixpkgs.follows = "nixpkgs";

      lanzaboote.url = "github:nix-community/lanzaboote";
      lanzaboote.inputs.nixpkgs.follows = "nixpkgs";

      nixos-hardware.url = "github:nixos/nixos-hardware";
      nixos-facter-modules.url = "github:numtide/nixos-facter-modules";

      nix-index-database.url = "github:nix-community/nix-index-database";
      nix-index-database.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    {
      self,
      lix-module,
      nixpkgs,
      disko,
      nixos-facter-modules,
      ...
    }@inputs:
    {
      nixosConfigurations = {
        "proxmox" = self.lib.nixosSystem "proxmox";
        "sea1-core" = self.lib.nixosSystem "sea1-core";
        "fmt2-core" = self.lib.nixosSystem "fmt2-core";
        "sea420-desktop" = self.lib.nixosSystem "sea420-desktop";
        "sea1-nix-builder" = self.lib.nixosSystem "sea1-nix-builder";
      };

      nixosModules = {
        base = import ./machines/base.nix;
      };

      # Per-host installer ISOs carrying the host's network config and SSH
      # keys, so the live environment comes up reachable at the host's
      # address for nixos-anywhere. Build/upload via `just make-installer`.
      installerImages = builtins.mapAttrs (
        machineName: _: self.lib.installerSystem machineName
      ) self.nixosConfigurations;

      lib = {
        vars = {
          machines = import ./vars/machines.nix inputs;
        };

        nixosSystem =
          machineName: self.lib.nixosSystem' machineName ./machines/${machineName}/configuration.nix;

        nixosSystem' =
          machineName: machineModule:
          nixpkgs.lib.nixosSystem {
            modules = [
              { networking.hostName = machineName; }
              # inputs.sops-nix.nixosModules.default
              self.nixosModules.base
              machineModule
              lix-module.nixosModules.default
              inputs.nix-index-database.nixosModules.nix-index
            ];
            specialArgs = {
              inherit self inputs;
              vars = self.lib.vars;
              vars' = self.lib.vars.machines.${machineName} or { };
            };
          };

        nixosModule =
          # Return the path (not `import`) so the module system can
          # deduplicate a module that is imported from several places.
          name:
          if builtins.pathExists ./modules/${name}/default.nix then
            ./modules/${name}/default.nix
          else if builtins.pathExists ./modules/${name}.nix then
            ./modules/${name}.nix
          else
            throw "NixOS module '${name}' not found in modules directory";

        diskoConfiguration =
          machineName:
          import ./machines/${machineName}/disko.nix {
            inherit (nixpkgs) lib;
          };

        sdImageFromSystem = system: system.config.system.build.sdImage;

        # Minimal installer ISO for a machine: copies the evaluated
        # systemd-networkd config and root authorized keys out of the
        # host's nixosConfiguration so the live system boots with the
        # host's addressing and accepts the same SSH keys.
        installerSystem =
          machineName:
          let
            hostConfig = self.nixosConfigurations.${machineName}.config;
          in
          (nixpkgs.lib.nixosSystem {
            system = self.nixosConfigurations.${machineName}.pkgs.stdenv.hostPlatform.system;
            modules = [
              "${nixpkgs}/nixos/modules/installer/cd-dvd/installation-cd-minimal.nix"
              {
                networking.hostName = "${machineName}-installer";
                image.baseName = nixpkgs.lib.mkForce "${machineName}-installer";

                # The host's network identity.
                networking.useDHCP = nixpkgs.lib.mkForce hostConfig.networking.useDHCP;
                systemd.network = {
                  inherit (hostConfig.systemd.network) enable netdevs networks;
                };

                services.openssh.enable = true;
                users.users.root.openssh.authorizedKeys.keys =
                  hostConfig.users.users.root.openssh.authorizedKeys.keys;
              }
            ];
          }).config.system.build.isoImage;

        # machineNixpkgsSystem fetches the architecture (pkgs.system) for a
        # given machine.
        machineNixpkgsSystem = machineName: self.nixosConfigurations.${machineName}.config.nixpkgs.system;
      };
  };
}
