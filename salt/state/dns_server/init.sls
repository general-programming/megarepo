dns_server_pkgs:
  pkg.installed:
    - pkgs:
      - dnsmasq

dns_server_default:
  file.managed:
    - name: /etc/dnsmasq.d/00-default.conf
    - source: salt://dns_server/files/00-default.j2
    - user: root
    - group: root
    - mode: '0644'
    - require:
      - pkg: dns_server_pkgs

dns_server_static:
  file.managed:
    - name: /etc/dnsmasq.d/99-dns.conf
    - source: salt://dns_server/files/99-dns.j2
    - user: root
    - group: root
    - mode: '0644'
    - require:
      - pkg: dns_server_pkgs

dns_server_service:
  service.running:
    - name: dnsmasq
    - enable: True
    - onchanges:
      - file: dns_server_default
      - file: dns_server_static
    - require:
      - pkg: dns_server_pkgs
