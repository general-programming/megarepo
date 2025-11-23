{{ saltenv }}:
  '*':
    - refresh_pillar
    - common
    - packages
    - groups
    - admin_user

  'G:tags:docker':
    - install_docker
