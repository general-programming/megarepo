import json
import os

from pyinfra import host
from pyinfra.operations import server, files, systemd

consul_datacenter = host.data.consul_datacenter

files.sync(
    name="Sync Consul consul-template configs",
    src="files/consul_vault/",
    dest="/etc/consul-template/templates/consul/",
)

files.template(
    name="Create Consul consul-template config.",
    src="templates/consul/consul_template.j2",
    dest="/etc/consul-template/config/10-consul.hcl",
    mode="644",
)

files.template(
    name="Create Consul config.",
    src="templates/consul/consul.hcl.j2",
    dest="/etc/consul.d/consul.hcl",
    mode="644",
    consul_datacenter=consul_datacenter,
    consul_servers=json.dumps(host.data.consul_servers)
)

server.service(
    name="Restart Consul.",
    service="consul",
    running=True,
    restarted=True,
    enabled=True,
)

server.service(
    name="Start and enable Consul.",
    service="consul",
    running=True,
    enabled=True,
)

server.service(
    name="Reload Consul.",
    service="consul",
    running=True,
    enabled=True,
    reloaded=True,
)
