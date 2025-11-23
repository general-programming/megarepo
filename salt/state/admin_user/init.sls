{% set admin_user = salt['pillar.get']('admin_user:username', 'admin') %}

admin_user_create:
  user.present:
    - name: {{ admin_user }}
    - home: /home/{{ admin_user }}
    - shell: /bin/bash
    - createhome: True

admin_user_ssh_keys:
  ssh_auth.manage:
    - user: {{ admin_user }}
    - source: salt://admin_user/files/authorized_keys_{{ admin_user }}
    - config: '/%h/.ssh/authorized_keys'
    - ssh_keys: []

admin_ssh_sudoers:
  file.managed:
    - name: /etc/sudoers.d/00-admin
    - mode: '0440'
    - user: root
    - group: root
    - contents: |
        '{{ admin_user }} ALL=(ALL) NOPASSWD: ALL'
    - require:
      - user: admin_user_create
