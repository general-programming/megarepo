{% if grains['os'] == 'Ubuntu' %}
{% set docker_key = "https://download.docker.com/linux/ubuntu/gpg" %}
{% elif grains['os'] == 'Debian' %}
{% set docker_key = "https://download.docker.com/linux/debian/gpg" %}
{% endif %}

install_docker_repo:
  pkgrepo.managed:
    - name: deb https://download.docker.com/linux/{{ grains['os'] | lower }} {{ grains['oscodename'] }} stable
    - file: /etc/apt/sources.list.d/docker.list
    - dist: {{ grains['oscodename'] }}
    - key_url: {{ docker_key }}
    - require_in:
      - pkg: install_docker_pkgs

install_docker_pkgs:
  pkg.installed:
    - names:
      - docker-ce
      - docker-buildx-plugin
      - docker-compose-plugin
