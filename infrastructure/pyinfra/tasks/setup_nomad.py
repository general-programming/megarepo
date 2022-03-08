import ipaddress
import json
import os

from pyinfra import host
from pyinfra.operations import files, server, systemd

approle_name = host.data.vault_role or "cluster-node"
is_server = approle_name == "cluster-server"

# Config for client/server
if is_server:
    config_type = "01-server"
else:
    config_type = "01-client"

# Get public IPv4 address
v4_addr = None

# Find the public IPv6 address of the machine.
for dev, dev_config in host.fact.network_devices.items():
    if dev_config["ipv4"]:
        dev_v4_addr = ipaddress.IPv4Address(dev_config["ipv4"]["address"])
        if dev_v4_addr.is_global:
            v4_addr = dev_v4_addr
            break

files.file(
    name="Delete /etc/nomad.d/nomad.hcl",
    path="/etc/nomad.d/nomad.hcl",
    present=False,
)

files.sync(
    name="Sync Nomad template configs",
    src="files/nomad_vault/",
    dest="/etc/consul-template/templates/nomad/",
)

files.sync(
    name="Sync Nomad static configs",
    src="files/nomad/config/",
    dest="/etc/nomad.d/",
)

files.template(
    name=f"Create Nomad config { config_type }.hcl",
    src=f"templates/nomad/{ config_type }.j2",
    dest=f"/etc/nomad.d/{ config_type }.hcl",
    mode="644",
    public_ip=v4_addr,
    nomad_servers=json.dumps(host.data.nomad_servers),
)

files.template(
    name="Create Nomad Consul Template config.",
    src="templates/nomad/consul_template.j2",
    dest="/etc/consul-template/config/10-nomad.hcl",
    mode="644",
    approle_name=approle_name,
)

if is_server:
    import hvac

    hvac_client = hvac.Client()

    vault_token = hvac_client.create_token(
        policies=["nomad-server"], period="72h", orphan=True
    )["auth"]["client_token"]
else:
    vault_token = None

files.template(
    name="Create Nomad Vault config.",
    src="templates/nomad/vault.j2",
    dest="/etc/nomad.d/20-vault.hcl",
    mode="644",
    vault_token=vault_token,
    vault_url=host.data.vault_url,
)

files.put(
    name="Create Nomad service.",
    src="files/nomad/systemd-nomad.service",
    dest="/etc/systemd/system/nomad.service",
    mode="644",
)
systemd.daemon_reload()

server.service(
    name="Restart Consul Template.",
    service="consul-template",
    running=True,
    restarted=True,
    enabled=True,
)

server.service(
    name="Restart Nomad.", service="nomad", running=True, enabled=True, restarted=True
)