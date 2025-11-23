# packages that share names between redhat + debian based systems
packages_common:
  pkg.installed:
    - pkgs:
      - apt-transport-https
      - ca-certificates
      - curl
      - software-properties-common
      - sudo
      - htop
      - tmux
      - byobu
      - mtr
      - gnupg
      - unzip
      - zsh
