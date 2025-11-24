# {{ pillar['common']['salt_managed'] }}

data_dir = "/var/lib/consul"
log_level = "INFO"
enable_local_script_checks = true
datacenter = "{{ grains['datacenter'] }}"
retry_join = {{ salt['pillar.get']('consul:retry_join', []) | json }}
alt_domain = "consul.generalprogramming.org"
{% if salt['pillar.get']('consul:bind_addr', '') -%}
bind_addr = "{{ salt['pillar.get']('consul:bind_addr') }}"
{% else -%}
{% raw %}
bind_addr = "{{ GetPrivateInterfaces | include \"network\" \"10.0.0.0/8\" | attr \"address\" }}"
{% endraw %}
{% endif -%}
