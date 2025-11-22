import time

import hvac
from proxmoxer import ProxmoxAPI

THIRTY_ONE_DAYS = 60 * 60 * 24 * 31
vault = hvac.Client()

proxmox_secrets = vault.secrets.kv.v2.read_secret_version(
    path="proxmox-cert-updater",
)["data"]["data"]

proxmox = ProxmoxAPI(
    "proxmox.service.fmt2.consul",
    user=proxmox_secrets["user"],
    token_name=proxmox_secrets["token_name"],
    token_value=proxmox_secrets["token_value"],
    verify_ssl=False,
)


def get_proxmox_hosts():
    nodes = []

    for node in proxmox.cluster.config.nodes.get():
        nodes.append(node)

    return nodes


for node in get_proxmox_hosts():
    node_hostname = node["node"] + ".generalprogramming.org"
    remaining_cert_time = 0

    proxmox_node = proxmox.nodes(node["node"])
    for cert in proxmox_node.certificates.info.get():
        if cert["filename"] != "pveproxy-ssl.pem":
            continue

        remaining_cert_time = cert["notafter"] - time.time()

    if remaining_cert_time > THIRTY_ONE_DAYS:
        print("Certificate for node {} is good.".format(node_hostname))
        continue

    print("Updating certificate on node {}".format(node_hostname))

    alt_names = [
        node_hostname,
    ]

    generate_certificate_response = vault.secrets.pki.generate_certificate(
        mount_point="pki_internal",
        name="genprog",
        common_name="proxmox-fmt2.generalprogramming.org",
        extra_params=dict(
            alt_names=",".join(alt_names),
            ttl="8766h",
        ),
    )
    print("Generated certificate for {}".format(node_hostname))

    chain = [generate_certificate_response["data"]["certificate"]]
    chain.extend(generate_certificate_response["data"]["ca_chain"])
    priv_key = generate_certificate_response["data"]["private_key"]

    cert_options = {
        "certificates": "\n".join(chain),
        "node": node["node"],
        "key": priv_key,
        "force": 1,
        "restart": 1,
    }

    proxmox_node.certificates.custom.post(**cert_options)
    print("Certificate updated on node {}".format(node_hostname))
