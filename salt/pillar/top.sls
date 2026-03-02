{{ saltenv }}:
  '*':
    - common
    - schedule
    - admin_user
    - firewalld
    - consul
    - install_docker
