{% set is_master = 'saltmaster' in salt['grains.get']('tags', []) %}
{% set salt_pkgs = ['salt-minion'] + (['salt-master'] if is_master else []) %}

{% if grains['os_family'] == 'RedHat' %}
salt_minion_repo:
  file.managed:
    - name: /etc/yum.repos.d/salt.repo
    - source: salt://salt_minion/files/salt.repo.j2
    - template: jinja
    - user: root
    - group: root
    - mode: '0644'

salt_minion_repo_refresh:
  cmd.run:
    - name: dnf clean expire-cache
    - onchanges:
      - file: salt_minion_repo

salt_minion_pkg:
  pkg.latest:
    - names: {{ salt_pkgs }}
    - refresh: True
    - require:
      - file: salt_minion_repo

{% elif grains['os_family'] == 'Debian' %}
salt_minion_keyring_dir:
  file.directory:
    - name: /etc/apt/keyrings
    - user: root
    - group: root
    - mode: '0755'
    - makedirs: True

salt_minion_keyring:
  cmd.run:
    - name: curl -fsSL https://packages.broadcom.com/artifactory/api/security/keypair/SaltProjectKey/public | gpg --yes --dearmor -o /etc/apt/keyrings/salt-archive-keyring.pgp # noqa: 204
    - unless: test -s /etc/apt/keyrings/salt-archive-keyring.pgp && ! grep -qa 'BEGIN PGP' /etc/apt/keyrings/salt-archive-keyring.pgp
    - require:
      - file: salt_minion_keyring_dir

salt_minion_repo:
  file.managed:
    - name: /etc/apt/sources.list.d/salt.sources
    - source: salt://salt_minion/files/salt.sources.j2
    - template: jinja
    - user: root
    - group: root
    - mode: '0644'
    - require:
      - cmd: salt_minion_keyring

salt_minion_pin:
  file.managed:
    - name: /etc/apt/preferences.d/salt-pin-1001
    - source: salt://salt_minion/files/salt-pin.j2
    - template: jinja
    - user: root
    - group: root
    - mode: '0644'

salt_minion_pkg:
  pkg.latest:
    - names: {{ salt_pkgs }}
    - refresh: True
    - require:
      - file: salt_minion_repo
      - file: salt_minion_pin
{% endif %}

# salt onedir package upgrades wipe salt-pip installed site-packages, which
# breaks vault_ssh/gitfs integrations until these are reinstalled
salt_minion_pip_deps:
  cmd.run:
    - name: salt-pip install saltext-vault gql aiohttp
    - unless: salt-pip show saltext-vault gql aiohttp
    - require:
      - pkg: salt_minion_pkg

salt_minion_service:
  service.running:
    - name: salt-minion
    - enable: True
    - watch:
      - pkg: salt_minion_pkg
      - cmd: salt_minion_pip_deps

{% if is_master %}
salt_master_pip_deps:
  cmd.run:
    - name: salt-pip install pygit2 saltext-vault
    - unless: salt-pip show pygit2 saltext-vault
    - require:
      - pkg: salt_minion_pkg

salt_master_service:
  service.running:
    - name: salt-master
    - enable: True
    - watch:
      - pkg: salt_minion_pkg
      - cmd: salt_master_pip_deps
{% endif %}
