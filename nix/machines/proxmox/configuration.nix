{
  self,
  inputs,
  config,
  ...
}:

let
  inherit (inputs)
    disko
    ;
in

{
  imports = [
    disko.nixosModules.disko

    (self.lib.nixosModule "disk/single")
    (self.lib.nixosModule "hardware/proxmox-vm")
    (self.lib.nixosModule "gitops")
    (self.lib.nixosModule "glances-tty")
    (self.lib.nixosModule "impermanence")
    # (self.lib.nixosModule "network")
    # (self.lib.nixosModule "ssh")
    (self.lib.nixosModule "secureboot")
  ];

  gitops = {
    enable = false;
    ref = "main";
  };

  nixpkgs.system = "x86_64-linux";
  system.stateVersion = "26.05"; # do not change
}
