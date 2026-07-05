sysctl_swappiness:
  sysctl.present:
    - name: vm.swappiness
    - value: 1
    - config: /etc/sysctl.d/10-salt.conf
