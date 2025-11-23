sshd_config_vault_ca:
  file.managed:
    - name: /etc/ssh/ssh_vault_ca.pub
    - contents: {{ salt['vault_ssh.read_ca']('ssh-client-signer') }}
    - user: root
    - group: root
    - mode: '0644'
