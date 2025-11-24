{% if 'service_proxmox' in salt['grains.get']('tags', []) %}
consul_service_proxmox:
  file.managed:
    - name: /etc/consul.d/service_proxmox.json
    - source: salt://consul/files/services/service_proxmox.hcl
    - user: root
    - group: root
    - mode: '0644'
{% endif %}
