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

    # services
    - service_node_exporter

  'G@tags:docker':
    - install_docker

  'G@tags:managed_firewall':
    - firewalld

  'G@tags:dnsserver':
    - dns_server
