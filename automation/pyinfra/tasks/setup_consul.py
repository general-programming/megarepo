import json
import os

from pyinfra import host
from pyinfra.facts.server import LinuxName
from pyinfra.operations import apt, files, server, systemd

consul_datacenter = host.data.consul_datacenter

if host.get_fact(LinuxName) in ["Debian", "Ubuntu"]:
    server.shell(
        name="Install HashiCorp repo",
        commands='apt-add-repository "deb [arch=amd64] https://apt.releases.hashicorp.com $(lsb_release -cs) main"',
    )

    apt.packages(
        name="Install Consul.",
        packages=[
            "consul",
        ],
        update=True,
    )

    for cert_file in ["server.crt", "server.key", "ca.crt"]:
        files.file(
            name="Touch Consul TLS files.",
            path="/etc/consul.d/" + cert_file,
            present=True,
            user="consul",
            group="consul",
            touch=True,
        )


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
    consul_servers=json.dumps(host.data.consul_servers),
    consul_server=host.data.consul_server or False,
)

server.service(
    name="Start and enable Consul.",
    service="consul",
    running=True,
    enabled=True,
)

server.service(
    name="Restart Consul.",
    service="consul",
    running=True,
    restarted=True,
    enabled=True,
)
