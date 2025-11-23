# initial code sourced from https://ornlu-is.github.io/overengineering_4/
{% if not salt['file.file_exists']('/usr/local/bin/node_exporter') %}

{% set node_exporter_version = "1.10.2" %}
retrieve_node_exporter:
  cmd.run:
    - name: wget -O /tmp/node_exporter.tar.gz https://github.com/prometheus/node_exporter/releases/download/v{{ node_exporter_version }}/node_exporter-{{ node_exporter_version }}.linux-amd64.tar.gz # noqa: 204

extract_node_exporter:
  archive.extracted:
    - name: /tmp
    - enforce_toplevel: false
    - source: /tmp/node_exporter.tar.gz
    - archive_format: tar
    - user: root
    - group: root

move_node_exporter:
  file.rename:
    - name: /usr/local/bin/node_exporter
    - source: /tmp/node_exporter-{{ node_exporter_version }}.linux-amd64/node_exporter

delete_node_exporter_dir:
  file.absent:
    - name: /tmp/node_exporter-{{ node_exporter_version }}.linux-amd64

delete_node_exporter_files:
  file.absent:
    - name: /tmp/node_exporter.tar.gz

{% endif %}

node_exporter_group:
  group.present:
    - name: node-exp
    - system: True

node_exporter_user:
  user.present:
    - name: node-exp
    - groups:
      - node-exp
    - shell: /sbin/nologin
    - system: True
    - createhome: False
    - home: /var/lib/node_exporter

node_exporter_service_file:
  file.managed:
    - name: /etc/systemd/system/node_exporter.service
    - source: salt://node-exporter/files/node_exporter.service
    - user: root
    - group: root
    - mode: '0644'

node_exporter_service_enable:
  cmd.run:
    - name: systemctl daemon-reload
    - watch:
      - file: /etc/systemd/system/node_exporter.service

node_exporter_service:
  service.running:
    - name: node_exporter
    - enable: True
    - restart: True
    - watch:
      - file: node_exporter_service_file
      - user: node_exporter_user
