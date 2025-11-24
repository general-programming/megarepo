dhcp_server_webhook:
  file.managed:
    - name: /usr/local/bin/dhcpd_discordhook
    - source: salt://dhcp_server/files/dhcpd_discordhook.py.j2
    - template: jinja
    - mode: '0755'
    - user: root
    - group: root

dhcp_server_apparmor:
  file.managed:
    - name: /etc/apparmor.d/local/usr.sbin.dhcpd
    - source: salt://dhcp_server/files/apparmor_dhcpd
    - user: root
    - group: root
    - mode: '0644'

dhcp_server_apparmor_update:
  cmd.run:
    - name: apparmor_parser -r /etc/apparmor.d/usr.sbin.dhcpd
    - onchanges:
      - file: dhcp_server_apparmor

# remove old ansible configs
dhcpd_ansible_cleanup:
  file.absent:
    - name: /etc/dhcp/dhcpd.d/00-ansible.conf

dhcpd6_ansible_cleanup:
  file.absent:
    - name: /etc/dhcp/dhcpd6.d/00-ansible.conf

# dhcpv4
dhcpd_config:
  file.managed:
    - name: /etc/dhcp/dhcpd.d/10-netbox.conf
    - source: salt://dhcp_server/files/dhcpd.conf.j2
    - template: jinja
    - user: root
    - group: root
    - mode: '0644'

dhcp_server_service:
  service.running:
    - name: isc-dhcp-server
    - enable: True
    - full_restart: True
    - require:
      - file: dhcp_server_webhook
      - cmd: dhcp_server_apparmor_update
    - onchanges:
      - file: dhcpd_config

# dhcpv6
dhcpd6_config:
  file.managed:
    - name: /etc/dhcp/dhcpd6.d/10-netbox.conf
    - source: salt://dhcp_server/files/dhcpd6.conf.j2
    - template: jinja
    - user: root
    - group: root
    - mode: '0644'
    - makedirs: True

dhcp6_server_service:
  service.running:
    - name: isc-dhcp-server6
    - enable: True
    - full_restart: True
    - require:
      - file: dhcp_server_webhook
      - cmd: dhcp_server_apparmor_update
    - onchanges:
      - file: dhcpd6_config
