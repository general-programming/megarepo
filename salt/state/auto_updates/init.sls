{% if not grains.get('proxmox', False) %}
{% if grains['os_family'] == 'RedHat' %}
auto_updates_pkg:
  pkg.installed:
    - name: dnf-automatic

auto_updates_config:
  file.managed:
    - name: /etc/dnf/automatic.conf
    - source: salt://auto_updates/files/automatic.conf.j2
    - template: jinja
    - user: root
    - group: root
    - mode: '0644'
    - require:
      - pkg: auto_updates_pkg

auto_updates_timer:
  service.running:
    - name: dnf-automatic.timer
    - enable: True
    - watch:
      - file: auto_updates_config
    - require:
      - pkg: auto_updates_pkg

{% elif grains['os_family'] == 'Debian' %}
auto_updates_pkg:
  pkg.installed:
    - pkgs:
      - unattended-upgrades
      - apt-listchanges

auto_updates_config:
  file.managed:
    - name: /etc/apt/apt.conf.d/20auto-upgrades
    - source: salt://auto_updates/files/20auto-upgrades.j2
    - template: jinja
    - user: root
    - group: root
    - mode: '0644'
    - require:
      - pkg: auto_updates_pkg

auto_updates_timer:
  service.running:
    - name: apt-daily-upgrade.timer
    - enable: True
    - watch:
      - file: auto_updates_config
    - require:
      - pkg: auto_updates_pkg
{% endif %}
{% endif %}
