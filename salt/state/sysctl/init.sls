sysctl_swappiness:
  sysctl.present:
    - name: vm.swappiness
    - value: 1
    - config: /eyc/sysctl.d/10-salt.conf
