{
  self,
  inputs,
  config,
  pkgs,
  ...
}:

let
  inherit (inputs)
    disko
    ;
in

{
  system.stateVersion = "26.05";

  imports = [
    disko.nixosModules.disko

    (self.lib.nixosModule "dns")
    (self.lib.nixosModule "gitops")
    (self.lib.nixosModule "glances-tty")
    (self.lib.nixosModule "impermanence")
    # (self.lib.nixosModule "network")
    # (self.lib.nixosModule "ssh")
    # (self.lib.nixosModule "secureboot")
    (self.lib.nixosModule "nvidia")

    ./hardware.nix
    ./boot.nix
    ./disko.nix
    ./nfs.nix
    ./tftp.nix
  ];

  gitops = {
    enable = false;
    ref = "main";
  };

  networking = {
    hostName = "sea420-desktop";
    domain = "generalprogramming.org";
    hostId = "30b7aad6";
  };

  # Latest kernel for hardware support; build the zfs_unstable kernel module
  # against it even when newer than ZFS officially supports. The `broken` gate
  # is on the module (not userland), so we override modulePackage. Note: the
  # intended `.override { enableUnsupportedExperimentalKernel = true; }` is
  # silently dropped here (unstable.nix swallows it via `...`@args), so patch
  # the derivation directly: add the configure flag and clear `broken`.
  boot = {
    kernelPackages = pkgs.linuxPackages_latest;
    zfs.package = pkgs.zfs_unstable;
    zfs.modulePackage = config.boot.kernelPackages.zfs_unstable.overrideAttrs (old: {
      configureFlags = (old.configureFlags or [ ]) ++ [ "--enable-linux-experimental" ];
      meta = old.meta // {
        broken = false;
      };
    });
  };

  # Networking
  networking.useDHCP = true;

  # Enable impermanence via /persist:
  impermanence.enable = true;
  fileSystems."/persist".neededForBoot = true;

  # Enable podman
  virtualisation = {
    containers.enable = true;
    podman = {
      enable = true;
      dockerCompat = true;
    };
    oci-containers.backend = "podman";
  };

  # punch holes for ports we might play with
  networking.firewall.allowedTCPPorts = [
      8000
      8080
  ];

  # Packages
  environment.systemPackages = with pkgs; [
    # other useful tools
    gnumake
    ubootTools
    screen
    # useful dev tools
    direnv
    python315
    tcpdump
    # human interactions with podman
    podman
    # flexing
    fastfetch
    # KDE
    kdePackages.discover # Optional: Install if you use Flatpak or fwupd firmware update sevice
    kdePackages.kcalc # Calculator
    kdePackages.kcharselect # Tool to select and copy special characters from all installed fonts
    kdePackages.kclock # Clock app
    kdePackages.kcolorchooser # A small utility to select a color
    kdePackages.kolourpaint # Easy-to-use paint program
    kdePackages.ksystemlog # KDE SystemLog Application
    kdePackages.sddm-kcm # Configuration module for SDDM
    kdiff3 # Compares and merges 2 or 3 files or directories
    kdePackages.isoimagewriter # Optional: Program to write hybrid ISO files onto USB disks
    kdePackages.partitionmanager # Optional: Manage the disk devices, partitions and file systems on your computer
    # Non-KDE graphical packages
    hardinfo2 # System information and benchmarks for Linux systems
    vlc # Cross-platform media player and streaming server
    wayland-utils # Wayland utilities
    wl-clipboard # Command-line copy/paste utilities for Wayland
    chromium
    gamescope-wsi
    steam-run

  ];

  programs.firefox = {
    enable = true;
  };

  # Desktop
  services.desktopManager.plasma6.enable = true;

  # Default display manager for Plasma
  services.displayManager.sddm = {
    enable = true;
    
    # To use Wayland (Experimental for SDDM)
    wayland.enable = true;
  };

  # Audio
  security.rtkit.enable = true;
  services.pipewire = {
    enable = true; # if not already enabled
    alsa.enable = true;
    alsa.support32Bit = true;
    pulse.enable = true;
    systemWide = true;
    # If you want to use JACK applications, uncomment the following
    #jack.enable = true;
  };

  # Gaming
  programs.steam = {
    enable = true; # Master switch, already covered in installation
    remotePlay.openFirewall = true;  # Open ports in the firewall for Steam Remote Play
    dedicatedServer.openFirewall = true; # Open ports for Source Dedicated Server hosting
    # Other general flags if available can be set here.
  };

  programs.gamescope = {
    enable = true;
    capSysNice = true;
  };

  # Tip: For improved gaming performance, you can also enable GameMode:
  programs.gamemode.enable = true;

  # Users
  users.users.meow = {
    isNormalUser = true;
    extraGroups = [
      "wheel"
      "networkmanager"
      "video"
      "audio"
      "pipewire"
    ];
    hashedPasswordFile = "/persist/passwords/meow";
  };

  # Disable auto sleep
  systemd.sleep.settings.Sleep = {
    AllowSuspend = "no";
    AllowHibernation = "no";
    AllowHybridSleep = "no";
    AllowSuspendThenHibernate = "no";
  };

  # Enable tailscale for this host only
  services.tailscale = {
    enable = true;
    useRoutingFeatures = "server";
  };
  
  # Allow other arch emulation
  boot.binfmt.emulatedSystems = [ "aarch64-linux" ];
  boot.binfmt.preferStaticEmulators = true;
  boot.binfmt.registrations."aarch64-linux".fixBinary = true;

  # dev env
  programs.direnv = {
    enable = true;
    nix-direnv.enable = true;
  };
}
