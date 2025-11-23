sshd_config_include:
  file.append:
    - name: /etc/ssh/sshd_config
    - text: 'Include /etc/sshd/sshd_config.d/*.conf'

sshd_config_vault_ca:
  file.managed:
    - name: /etc/ssh/ssh_vault_ca.pub
    - contents: {{ salt['vault_ssh.read_ca']('ssh-client-signer') }}
    - user: root
    - group: root
    - mode: '0644'

sshd_config_managed:
  file.managed:
    - name: /etc/sshd/sshd_config.d/10-genprog.conf
    - source: salt://sshd_config/files/10-genprog.conf.j2
    - template: jinja
    - user: root
    - group: root
    - mode: '0644'
    - makedirs: True

sshd_config_remove_old:
  file.absent:
    - name: /etc/ssh/sshd_config.d/00-ansible.conf

sshd_config_restart:
  service.running:
    - name: sshd
    - enable: True
    - onchanges:
      - file: sshd_config_include
      - file: sshd_config_managed
      - file: sshd_config_vault_ca
