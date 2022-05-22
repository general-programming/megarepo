from pyinfra import host
from pyinfra.facts.server import LinuxName
from pyinfra.operations import files, server, systemd

files.template(
    name="Create Consul Template base config.",
    src="templates/consul_template/base.j2",
    dest="/etc/consul-template/config/00-base.hcl",
    mode="644",
    vault_url=host.data.vault_url,
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
