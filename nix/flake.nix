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

      lanzaboote.url = "github:rv32ima/lanzaboote";
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
      };

      nixosModules = {
        base = import ./machines/base.nix;
      };

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
          name:
          if builtins.pathExists ./modules/${name}/default.nix then
            import ./modules/${name}/default.nix
          else if builtins.pathExists ./modules/${name}.nix then
            import ./modules/${name}.nix
          else
            throw "NixOS module '${name}' not found in modules directory";

        diskoConfiguration =
          machineName:
          import ./machines/${machineName}/disko.nix {
            inherit (nixpkgs) lib;
          };

        sdImageFromSystem = system: system.config.system.build.sdImage;

        # machineNixpkgsSystem fetches the architecture (pkgs.system) for a
        # given machine.
        machineNixpkgsSystem = machineName: self.nixosConfigurations.${machineName}.config.nixpkgs.system;
      };
  };
}
