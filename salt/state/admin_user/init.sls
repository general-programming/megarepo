{% set admin_user = salt['pillar.get']('admin_user:username', 'admin') %}

admin_user_create:
  user.present:
    - name: {{ admin_user }}
    - home: /home/{{ admin_user }}
    - shell: /bin/bash
    - createhome: True
    - remove_groups: False
    - groups:
      - wheel

admin_user_ssh_keys:
  ssh_auth.manage:
    - user: {{ admin_user }}
    - config: '/%h/.ssh/authorized_keys'
    - ssh_keys:
      - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAID/AoFk4QgHsVnYa4o2YYPxQW1TGvDNXO9sY7VRfM1lI infra-admin

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
    - unless: test -f /home/{{ admin_user }}/.byobu
    - require:
      - user: admin_user_create
