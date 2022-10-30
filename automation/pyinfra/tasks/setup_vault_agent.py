import os

from pyinfra import host
from pyinfra.facts.server import LinuxName
from pyinfra.operations import files, server, systemd

approle_name = host.data.get("vault_role", "cluster-node")

if "APPROLE_EXISTS" in os.environ:
    approle_id = None
    approle_secret = None
else:
    import hvac

    hvac_client = hvac.Client()

    if not hvac_client.is_authenticated():
        raise Exception("'vault login' is required to deploy.")

    approle_id = hvac_client.auth.approle.read_role_id(role_name=approle_name)["data"][
        "role_id"
    ]
    approle_secret = hvac_client.write(
        path=f"auth/approle/role/{ approle_name }/secret-id",
    )["data"]["secret_id"]

server.packages(
    name="Ensure Vault is installed.",
    packages=["vault"],
    present=True,
)

if approle_id:
    files.template(
        name="Create /opt/vault/approle_id",
        src="templates/line.j2",
        dest="/opt/vault/approle_id",
        user="vault",
        group="vault",
        mode="600",
        line=approle_id,
    )

if approle_secret:
    files.template(
        name="Create /opt/vault/approle_secret",
        src="templates/line.j2",
        dest="/opt/vault/approle_secret",
        user="vault",
        group="vault",
        mode="600",
        line=approle_secret,
    )

files.template(
    name="Create Vault config.",
    src="templates/vault-agent.j2",
    dest="/etc/vault.d/agent.hcl",
    user="vault",
    group="vault",
    mode="644",
    approle_name=approle_name,
    vault_url=host.data.vault_url,
)

if host.get_fact(LinuxName) == "Alpine":
    files.template(
        name="Update /etc/conf.d/vault",
        src="templates/line.j2",
        dest="/etc/conf.d/vault",
        user="root",
        group="root",
        mode="644",
        line='vault_opts="agent -config=/etc/vault.d/agent.hcl"',
    )

    server.service(
        name="Restart and enable the Vault agent",
        service="vault",
        running=True,
        restarted=True,
        enabled=True,
    )

else:
    # assume systemd by default because of its grasp over everything.
    files.put(
        name="Create Vault agent service.",
        src="files/vault-agent.service",
        dest="/etc/systemd/system/vault-agent.service",
        mode="644",
    )

    systemd.service(
        name="Restart and enable the Vault agent",
        service="vault-agent.service",
        running=True,
        restarted=True,
        enabled=True,
    )
