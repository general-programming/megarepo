{ ... }:

{
  boot = {
    kernelParams = [
      "console=tty0"
      "pcie_port_pm=off"
      "pcie_aspm.policy=performance"
    ];

    kernelModules = [
      "kvm-intel"
    ];

    kernel.sysctl = {
      "vm.swappiness" = 80;
    };

    supportedFilesystems = [
      "btrfs"
    ];

    initrd = {
      availableKernelModules = [
        "nvme"
        "xhci_pci"
        "ahci"
        "usbhid"
        "uas"
      ];
      kernelModules = [
        "dm-snapshot"
      ];
      systemd = {
        enable = true;
      };
    };

    loader = {
      systemd-boot.enable = true;
      efi.canTouchEfiVariables = true;
    };
  };
}
