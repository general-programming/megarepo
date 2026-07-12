# Set of sanity-keeping configurations.
# All machines should import this module.

{
  lib,
  self,
  pkgs,
  vars',
  inputs,
  ...
}:

{
  imports = [
    (self.lib.nixosModule "nixos-tags")
  ];

  # nix configs
  nix.settings.substituters = [
    "https://cache.nixos.org/"
    "https://nix-community.cachix.org"
  ];

  nix.settings.trusted-public-keys = [
    "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
  ];

  nixpkgs.config = {
    allowUnfree = true;
    problems.handlers = {
      zfs.broken = "ignore";
    };
  };

  # default boot settings
  boot.loader.systemd-boot = {
    enable = lib.mkDefault true;
    consoleMode = "auto";
  };
  boot.loader.efi.canTouchEfiVariables = lib.mkDefault true;

  systemd.settings.Manager = {
    # Don't wait too long for services to stop:
    DefaultTimeoutStopSec = "15s";
    # Prevent the system from hanging:
    RuntimeWatchdogSec = "5m";
    ShutdownWatchdogSec = "15m";
  };

  services.openssh = {
    enable = true;
    settings = {
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
    };
    openFirewall = true;
  };

  users.users.root.openssh.authorizedKeys.keys = [
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMVk9i7FG7dc9r4ixwAJT7uPLH3UuqbwIgeZ7Ytmnpvv erin-laptop"
  ];

  services.journald.extraConfig = ''
    SystemMaxUse=2G
    MaxRetentionSec=3month
  '';

  # We're using Flakes so this is a requirement.
  nix.settings = {
    experimental-features = [
      "nix-command"
      "flakes"
    ];
  };

  # Probably needed in some systems:
  hardware.enableRedistributableFirmware = true;

  # All overlays.
  nixpkgs.overlays = [
    (final: prev: {
      disko = inputs.disko.packages.${pkgs.system}.default;
    })
  ];

  # Useful tools.
  environment.systemPackages = with pkgs; [
    htop
    jq
    vim
    wget
    disko
    sbctl
    dig
    curl
    git
    mtr
    # persistent terminal sessions for ssh
    tmux
    # useful for getting metal host information
    dmidecode
  ];

  # nix-index with pre-built database (no local indexing needed)
  programs.nix-index-database.comma.enable = true;

  environment.sessionVariables = {
    FLAKE = self;
  };

  # Where we are roughly:
  time.timeZone = "America/Los_Angeles";

  # Enable LLDP for neighbor announcements on the network
  services.lldpd.enable = true;

  # Since impermanence currently screws up machine-id, manually override it to
  # whatever is in vars:
  environment.etc = lib.optionalAttrs (vars' ? machineID) { machine-id.text = vars'.machineID; };
  boot.kernelParams = lib.optional (vars' ? machineID) "systemd.machine_id=${vars'.machineID}";

  # Enable node-exporter by default for Prometheus monitoring.
  services.prometheus.exporters.node = {
    enable = true;
    openFirewall = true;
  };

  # Enable nix-index so that comma and command not found functions for humans.
  programs.nix-index.enable = true;

  # default tends to be x86
  nixpkgs.hostPlatform = lib.mkDefault "x86_64-linux";
  hardware.cpu.intel.updateMicrocode = true;
  hardware.cpu.amd.updateMicrocode = true;
}
