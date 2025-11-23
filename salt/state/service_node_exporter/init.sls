include:
  - service_node_exporter.install

node_exporter_consul_service:
  file.managed:
    - name: /etc/consul.d/service_node_exporter.hcl
    - source: salt://service_node_exporter/files/service_node_exporter.hcl
    - user: root
    - group: root
    - mode: '0644'
