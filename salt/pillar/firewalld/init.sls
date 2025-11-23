# tags:managed_firewall
{% if "managed_firewall" in salt['grains.get']('tags', []) %}
firewalld:
  enabled: true
  ipsets:
    internal_traffic4:
      short: internal_traffic4
      description: Internal IPv4 traffic
      type: hash:net
      options:
        maxelem:
          - 65536
        timeout:
          - 300
        hashsize:
          - 1024
      entries:
        - 10.0.0.0/8
        - 192.168.0.0/16
        - 172.16.0.0/12
    internal_traffic6:
      short: internal_traffic6
      description: Internal IPv6 traffic
      type: hash:net
      options:
        maxelem:
          - 65536
        timeout:
          - 300
        hashsize:
          - 1024
      entries:
        - 2602:fa6d:10::/48
        - 2620:fc:c000::/64
  services:
    salt-minion:
      short: salt-minion
      description: "salt-minion"
      ports:
        tcp:
          - "8000"
  zones:
    public:
      short: public
      description: Public Zone
      services:
        - http
        - https
        - ssh
        - salt-minion
        - dns
        - dhcpv6-client
{% if 'dnsserver' in salt['grains.get']('tags', []) %}
        - dns
{% endif %}
{% if 'dnsserver' in salt['grains.get']('tags', []) %}
        - dhcp
{% endif %}
      protocols:
        - icmp
        - ipv6-icmp
      rich_rules:
        - family: ipv4
          ipset:
            name: internal_traffic4
          accept: true
        - family: ipv6
          ipset:
            name: internal_traffic6
          accept: true
      ports:
        - comment: node-exporter
          port: 9100
          protocol: tcp
{% if 'saltmaster' in salt['grains.get']('tags', []) %}
        - comment: salt-master
          port: 4505
          protocol: tcp
        - comment: salt-python
          port: 4506
          protocol: tcp
{% endif %}
{% else %}
firewalld:
  enabled: false
{% endif %}
