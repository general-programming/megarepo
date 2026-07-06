"""Rendering network.yml hosts to device configs.

Shared by ``barf config generate|diff|deploy``.
"""

import os
from typing import List

import yaml

from barf.common import render_template
from barf.model.wireguard import WGNetworkLink, prefetch_keypairs
from barf.util.sites import SITE_ORIGIN_FUNC, site_import_rules
from barf.vendors import BaseHost


def prefetch_link_keys(targets: List[BaseHost], links: List[WGNetworkLink]) -> None:
    """Warm the WireGuard key cache for every link touching ``targets``.

    Rendering a host needs the keypairs of BOTH sides of each of its
    links; fetching them serially from Vault dominates render time, so
    pull them concurrently up front.
    """
    target_names = {host.hostname for host in targets}
    paths = set()
    for link in links:
        # IPsec links have no WireGuard keypairs in Vault.
        if link.ipsec:
            continue
        if link.side_a.hostname in target_names or link.side_b.hostname in target_names:
            paths.add(link._key_path(link.side_a.hostname))
            paths.add(link._key_path(link.side_b.hostname))

    prefetch_keypairs(sorted(paths))


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
        vpn_links = [
            link for link in links if host == link.side_a or host == link.side_b
        ]
        role_meta["vpn_links"] = vpn_links
        # Geographic weighting context: templates only format these
        # ready-made values into vendor syntax (barf/util/sites.py owns
        # the semantics). site_import_rules is empty when the host has
        # no site of its own.
        sites = global_meta.get("sites") or {}
        rules = site_import_rules(host, vpn_links, sites)
        role_meta["site_import_rules"] = rules
        role_meta["origin_site"] = sites.get(host.site) if host.site else None
        role_meta["community_asn"] = global_meta.get("community_asn")
        role_meta["SITE_ORIGIN_FUNC"] = SITE_ORIGIN_FUNC

        # bird cannot compose a standalone import_filter with the
        # generated site-weighted filters (filters cannot call
        # filters), so the combination would silently drop the
        # host's own filter on every weighted peer. Fail fast.
        if rules and (getattr(host, "bird", None) or {}).get("import_filter"):
            raise ValueError(
                f"{host.hostname}: bird.import_filter cannot be combined"
                " with site weighting; expose the check as a function and"
                " set bird.import_check_function instead"
            )

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
