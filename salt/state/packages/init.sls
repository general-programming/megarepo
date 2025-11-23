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
      - gnupg2
      - unzip
      - zsh

{% if grains['os_family'] == 'Debian' %}
packages_debian:
  pkg.installed:
    - pkgs:
      - apt-transport-https
{% if salt['grains.get']('osmajorrelease') != 13 %}
      # deb13 removed this?
      - software-properties-common
{% endif %}
{% endif %}
