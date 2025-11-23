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
    node-exporter:
      short: node-exporter
      description: "node-exporter"
      ports:
        tcp:
          - "9100"
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
        - node-exporter
      protocols:
        - icmp
        - ipv6-icmp
      rich_rules:
        - family: ipv4
          ipset:
            - name: internal_traffic4
          accept: true
        - family: ipv6
          ipset:
            - name: internal_traffic6
          accept: true
{% else %}
firewalld:
  enabled: false
{% endif %}
