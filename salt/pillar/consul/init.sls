consul:
{% if grains['datacenter'] == 'sea420' %}
  bootstrap_expect: 1
  retry_join:
    - "consul.service.{{ grains['datacenter'] }}.consul"
{% elif grains['datacenter'] == 'sea1' %}
  # The sea1 consul servers are no longer the Proxmox hypervisors
  # (2602:fa6d:10:ffff::101/102/103) -- those hosts were rebuilt as bare-metal
  # Talos nodes and the servers moved into k8s as a StatefulSet that publishes
  # 8300/8301/8302 as hostPorts. These are the k8s node addresses.
  #
  # No bind_addr override: the servers bind 0.0.0.0 and advertise their node's
  # IPv4, so the whole DC gossips over v4 and 00-base.hcl's default
  # 10.0.0.0/8 template is correct.
  retry_join:
    - "10.3.2.10"
    - "10.3.2.11"
    - "10.3.2.12"
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
