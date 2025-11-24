consul:
{% if grains['datacenter'] == 'sea420' %}
  retry_join: []
  bootstrap_expect: 1
{% elif grains['datacenter'] == 'sea1' %}
  retry_join:
    - "2602:fa6d:10:ffff::101"
    - "2602:fa6d:10:ffff::102"
    - "2602:fa6d:10:ffff::103"
  bind_addr: "[::]"
{% elif grains['datacenter'] == 'fmt2' %}
  retry_join:
    - "10.65.67.47"
    - "10.65.67.48"
    - "10.65.67.49"
{% else %}
  retry_join:
    - "consul.service.{{ grains['datacenter'] }}.consul"
{% endif %}
