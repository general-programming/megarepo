{% if grains['os_family'] == 'RedHat' %}
{% set sshd_service = 'sshd' %}
{% else %}
{% set sshd_service = 'ssh' %}
{% endif %}
{# vault_ssh disappears when a salt upgrade wipes salt-pip packages; skip the
   CA state instead of failing the whole highstate so salt_minion can heal #}
{% set has_vault_ssh = 'vault_ssh.read_ca' in salt %}

sshd_config_include:
  file.append:
    - name: /etc/ssh/sshd_config
    - text: 'Include /etc/sshd/sshd_config.d/*.conf'

{% if has_vault_ssh %}
sshd_config_vault_ca:
  file.managed:
    - name: /etc/ssh/ssh_vault_ca.pub
    - contents: {{ salt['vault_ssh.read_ca']('ssh-client-signer') }}
    - user: root
    - group: root
    - mode: '0644'
{% endif %}

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
    - name: {{ sshd_service }}
    - enable: True
    - watch:
      - file: sshd_config_include
      - file: sshd_config_managed
{% if has_vault_ssh %}
      - file: sshd_config_vault_ca
{% endif %}
