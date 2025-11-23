{{ saltenv }}:
  '*':
    - refresh_pillar
    - common
    - packages
    - groups
    - admin_user
    - sysctl
    - certs

  'G@tags:docker':
    - install_docker
