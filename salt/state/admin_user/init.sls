{% set admin_user = salt['pillar.get']('admin_user:username', 'admin') %}

# create a home directory in /opt since /home may be mapped to a remote filesystem
admin_user_create_home:
  file.directory:
    - name: /opt/home
    - user: root
    - group: root
    - mode: '0755'
    - makedirs: True

admin_user_create:
  user.present:
    - name: {{ admin_user }}
    - home: /opt/home/{{ admin_user }}
    - shell: /bin/bash
    - createhome: True
    - remove_groups: False
    - groups:
      - wheel
    - require:
      - file: admin_user_create_home

admin_user_ssh_keys:
  ssh_auth.manage:
    - user: {{ admin_user }}
    - config: '/%h/.ssh/authorized_keys'
    - ssh_keys:
      - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAID/AoFk4QgHsVnYa4o2YYPxQW1TGvDNXO9sY7VRfM1lI infra-admin
    - require:
      - user: admin_user_create

admin_user_sudoers:
  file.managed:
    - name: /etc/sudoers.d/00-admin
    - mode: '0440'
    - user: root
    - group: root
    - contents: |
        {{ admin_user }} ALL=(ALL) NOPASSWD: ALL
    - require:
      - user: admin_user_create

admin_user_enable_byobu:
  cmd.run:
    - name: byobu-enable
    - runas: {{ admin_user }}
    - unless: test -d /home/{{ admin_user }}/.config/byobu || test -d /home/{{ admin_user }}/.byobu
    - require:
      - user: admin_user_create
