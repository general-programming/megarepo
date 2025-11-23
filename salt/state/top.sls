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

  'G@tags:docker':
    - install_docker
