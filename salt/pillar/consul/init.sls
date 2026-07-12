consul:
{% if grains['datacenter'] == 'sea420' %}
  bootstrap_expect: 1
  retry_join:
    - "consul.service.{{ grains['datacenter'] }}.consul"
{% elif grains['datacenter'] == 'sea1' %}
  retry_join:
    - "2602:fa6d:10:ffff::101"
    - "2602:fa6d:10:ffff::102"
    - "2602:fa6d:10:ffff::103"
  bind_addr: "[::]"
{% elif grains['datacenter'] == 'fmt2' %}
  # consul servers on the fmt2 hypervisors
  bootstrap_expect: 5
  retry_join:
    - "10.65.67.100"
    - "10.65.67.101"
    - "10.65.67.102"
    - "10.65.67.103"
    - "10.65.67.104"
{% else %}
  retry_join:
    - "consul.service.{{ grains['datacenter'] }}.consul"
{% endif %}
