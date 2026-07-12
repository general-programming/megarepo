{# containerized: needs root + raw device access either way, and docker is
   already the daemon runtime on these hosts (cephadm); gated to proxmox
   hypervisors for now #}
{% set image = 'ghcr.io/prometheus-community/smartctl-exporter:v0.16.0' %}

{% if grains.get('proxmox', False) %}
smartctl_exporter_image:
  cmd.run:
    - name: docker pull {{ image }}
    - unless: docker image inspect {{ image }}

smartctl_exporter_unit:
  file.managed:
    - name: /etc/systemd/system/smartctl-exporter.service
    - source: salt://service_smartctl_exporter/files/smartctl-exporter.service.j2
    - template: jinja
    - context:
        image: {{ image }}
    - user: root
    - group: root
    - mode: '0644'

smartctl_exporter_daemon_reload:
  cmd.run:
    - name: systemctl daemon-reload
    - onchanges:
      - file: smartctl_exporter_unit

smartctl_exporter_service:
  service.running:
    - name: smartctl-exporter
    - enable: True
    - watch:
      - file: smartctl_exporter_unit
    - require:
      - cmd: smartctl_exporter_image
      - cmd: smartctl_exporter_daemon_reload

# legacy ansible-era textfile SMART collection, replaced by smartctl-exporter
smartmon_cron_cleanup:
  cmd.run:
    - name: crontab -l | grep -v prom_smartmon | crontab -
    - onlyif: crontab -l | grep -q prom_smartmon

smartmon_script_cleanup:
  file.absent:
    - name: /usr/local/bin/prom_smartmon.sh

smartmon_textfile_cleanup:
  file.absent:
    - name: /var/lib/node_exporter/smartmon.prom
{% endif %}
