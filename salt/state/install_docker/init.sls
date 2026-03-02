{% if grains['os'] == 'Ubuntu' %}
{% set docker_key = "https://download.docker.com/linux/ubuntu/gpg" %}
{% elif grains['os'] == 'Debian' %}
{% set docker_key = "https://download.docker.com/linux/debian/gpg" %}
{% endif %}

install_docker_repo:
  pkgrepo.managed:
    - name: deb [arch={{ grains['osarch'] }} signed-by=/usr/share/keyrings/docker-archive-keyring.asc] https://download.docker.com/linux/{{ grains['os'] | lower }} {{ grains['oscodename'] }} stable # noqa: 204
    - file: /etc/apt/sources.list.d/docker.list
    - dist: {{ grains['oscodename'] }}
    - key_url: {{ docker_key }}
    - aptkey: False
    - require_in:
      - pkg: install_docker_pkgs

install_docker_pkgs:
  pkg.installed:
    - names:
      - docker-ce
      - docker-buildx-plugin
      - docker-compose-plugin

{% if grains['os_family'] == 'Debian' and salt['pillar.get']('install_docker:manage_apparmor', False) %}
install_docker_apparmor_utils:
  pkg.installed:
    - name: apparmor-utils

install_docker_apparmor_profile:
  file.managed:
    - name: /etc/apparmor.d/docker-default
    - source: salt://install_docker/files/docker-default
    - user: root
    - group: root
    - mode: '0644'
    - require:
      - pkg: install_docker_apparmor_utils

install_docker_apparmor_complain:
  cmd.run:
    - name: aa-complain /etc/apparmor.d/docker-default
    - onchanges:
      - file: install_docker_apparmor_profile

install_docker_apparmor_reload:
  cmd.run:
    - name: apparmor_parser -r /etc/apparmor.d/docker-default
    - onchanges:
      - file: install_docker_apparmor_profile

install_docker_apparmor_disable:
  cmd.run:
    - name: aa-disable /etc/apparmor.d/docker-default
    - onchanges:
      - file: install_docker_apparmor_profile
{% endif %}
