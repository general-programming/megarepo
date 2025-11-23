# packages that share names between redhat + debian based systems
packages_common:
  pkg.installed:
    - pkgs:
      - ca-certificates
      - curl
      - sudo
      - htop
      - tmux
      - byobu
      - mtr
      - gnupg
      - unzip
      - zsh

{% if grains['os_family'] == 'Debian' %}
packages_debian:
  pkg.installed:
    - pkgs:
      - apt-transport-https
      - software-properties-common
{% endif %}
