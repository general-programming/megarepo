import hvac
from pyinfra import host
from pyinfra.facts.server import LinuxName
from pyinfra.operations import files, server, systemd


def generate_vault_token():
    hvac_client = hvac.Client()

    return hvac_client.create_token(
        policies=["vault-consul-tls-policy"],
        period="168h",
        orphan=True,
    )["auth"]["client_token"]


is_vault_core = host.data.get("consul_server") or host.data.get("vault_server")

files.template(
    name="Create Consul Template base config.",
    src="templates/consul_template/base.j2",
    dest="/etc/consul-template/config/00-base.hcl",
    mode="644",
    vault_url=host.data.vault_url,
    vault_token=generate_vault_token() if is_vault_core else None,
)

if host.get_fact(LinuxName) == "Alpine":
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
