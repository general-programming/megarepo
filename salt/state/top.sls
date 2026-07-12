{{ saltenv }}:
  '*':
    - refresh_pillar
    - common
    - packages
    - groups
    - admin_user
    - sysctl
    - certs
    - sshd_config
    - auto_updates

    # services
    - service_node_exporter
    - service_smartctl_exporter

  'G@tags:docker':
    - install_docker

  'G@tags:managed_firewall':
    - firewalld

  'G@tags:dnsserver':
    - dns_server

  'G@tags:dhcpserver':
    - dhcp_server

  'G@tags:consul':
    - consul
