import ipaddress
import json
import os

from pyinfra import host
from pyinfra.operations import files, server, systemd

files.template(
    name="Create Consul config.",
    src="templates/consul/node.j2",
    dest="/etc/consul.d/consul.hcl",
    mode="644",
    consul_datacenter=host.data.consul_datacenter,
    consul_servers=json.dumps(host.data.consul_servers),
)

files.put(
    name="Consul node_exporter template.",
    src="common/consul_configs/service_node_exporter.json",
    dest="/etc/consul.d/service_node_exporter.json",
    mode="644",
)

server.service(
    name="Restart Consul.",
    service="consul",
    running=True,
    restarted=True,
    enabled=True,
)
