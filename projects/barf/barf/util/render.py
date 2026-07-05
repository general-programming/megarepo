"""Rendering network.yml hosts to device configs.

Shared by ``barf config generate|diff|deploy``.
"""

import os
from typing import List

import yaml

from barf.common import render_template
from barf.model.wireguard import WGNetworkLink
from barf.vendors import BaseHost


def render_host_config(
    host: BaseHost,
    links: List[WGNetworkLink],
    global_meta: dict,
    secrets,
) -> str:
    """Render the config for one network.yml host.

    Args:
        host: The host to render.
        links: All network links; the host's VPN links are selected here.
        global_meta: The network.yml ``global_meta`` block.
        secrets: The Vault secret fetcher passed through to templates.
    """
    role_meta = {}
    if host.role == "vpn":
        role_meta["vpn_links"] = [
            link for link in links if host == link.side_a or host == link.side_b
        ]

    return render_template(
        f"{host.role}/{host.devicetype}.j2",
        device=host,
        secrets=secrets,
        global_meta=global_meta,
        **role_meta,
    )


def write_rendered_config(
    host: BaseHost, rendered: str, output_dir: str = "output"
) -> str:
    """Write a rendered config (and its cloud-init twin) under ``output_dir``.

    Returns:
        The path of the main config file.
    """
    role_dir = os.path.join(output_dir, host.role)
    os.makedirs(os.path.join(role_dir, "cloud_init"), exist_ok=True)

    config_path = os.path.join(role_dir, host.hostname)
    with open(config_path, "w") as f:
        f.write(rendered)

    with open(os.path.join(role_dir, "cloud_init", host.hostname), "w") as f:
        f.write("#cloud-config\n")
        yaml.dump(
            {"vyos_config_commands": [x for x in rendered.split("\n") if x]},
            f,
        )

    return config_path
