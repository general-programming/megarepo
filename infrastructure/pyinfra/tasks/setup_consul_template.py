import hvac

from pyinfra import host
from pyinfra.operations import server, files, systemd

vault_role = host.data.vault_role

if vault_role:
    hvac_client = hvac.Client()
    vault_token = hvac_client.create_token(role=vault_role, period="72h", orphan=True)["auth"]["client_token"]
else:
    vault_token = None

files.template(
    name="Create Consul Template base config.",
    src="templates/consul_template/base.j2",
    dest="/etc/consul-template/config/00-base.hcl",
    mode="644",
    vault_url=host.data.vault_url,
    vault_token=vault_token,
)

if host.fact.linux_distribution["name"] == "Alpine":
    server.packages(
        name="Ensure Consul Template is installed.",
        packages=["consul-template"],
        present=True,
    )

    files.link(
        name="Symlink consul-template.hcl to config.hcl",
        path="/etc/consul-template/consul-template.hcl",
        target="/etc/consul-template/configs",
    )
else:
    # lol systemd
    files.put(
        name="Install Consul Template binary.",
        src="files/consul-template",
        dest="/usr/local/sbin/consul-template",
        mode="755",
    )

    files.put(
        name="Create Consul Template service.",
        src="files/consul-template.service",
        dest="/etc/systemd/system/consul-template.service",
        mode="644",
    )

    systemd.daemon_reload()
