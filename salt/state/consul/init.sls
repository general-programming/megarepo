{% if grains['os_family'] == 'Debian' %}
consul_repo_debian:
  pkgrepo.managed:
    - name: "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg arch={{ grains['osarch'] }}] https://apt.releases.hashicorp.com {{ grains["lsb_distrib_codename"] }} main" # noqa: 204
    - dist: {{ grains['lsb_distrib_codename'] }}
    - file: /etc/apt/sources.list.d/hashicorp.list
    - key_url: https://apt.releases.hashicorp.com/gpg
    - aptkey: False
    - require_in:
      - pkg: consul_pkg
{% elif grains['os_family'] == 'RedHat' %}
{% if grains['os'] == 'Fedora' %}
  {% set hashi_release = 'fedora' %}
{% else %}
  {% set hashi_release = 'RHEL' %}
{% endif %}
consul_repo_redhat:
  file.managed:
    - name: /etc/yum.repos.d/hashicorp.repo
    - source: https://rpm.releases.hashicorp.com/{{ hashi_release }}/hashicorp.repo
    - require_in:
      - pkg: consul_pkg
{% endif %}

consul_pkg:
  pkg.installed:
    - name: consul

consul_config:
  file.recurse:
    - name: /etc/consul.d
    - source: salt://consul/files/consul_configs
    - user: root
    - group: root
    - template: jinja
    - file_mode: '0644'

# Debian default unit file expects consul.hcl to exist
consul_empty_consulhcl:
  file.managed:
    - name: /etc/consul.d/consul.hcl
    - contents: '# This file is intentionally left blank.'
    - user: root
    - group: root
    - mode: '0644'

# add services
include:
  - consul.services

consul_service:
  service.running:
    - name: consul
    - enable: True
    - full_restart: True
    - watch:
      - pkg: consul_pkg
      - file: consul_config
      - file: consul_empty_consulhcl
