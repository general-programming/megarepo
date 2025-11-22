import logging
import random
from typing import List, Optional

from zuscale.providers.base import BaseProvider, BaseServer, OSArch, VMImage
from zuscale.util import ServerType, SSHKey, get_env, to_b64

log = logging.getLogger("provider.vultr")


class Vultr(BaseProvider):
    NAME = "vultr"
    BASE_DOMAIN = "https://api.vultr.com/v2/"
    REQUESTS_LIMIT = 2

    def __init__(self, loop=None):
        self.token = get_env("VULTR_TOKEN")

        super().__init__(loop)

    def _headers(self) -> dict:
        return {"Authorization": "Bearer " + self.token}

    async def pre_hook(self, response):
        if "error" in response and response["error"]:
            raise Exception(response["error"])

    async def _list_types(self) -> List[ServerType]:
        result = {}
        server_types_result = await self.get("/plans")

        for server_type in server_types_result["plans"]:
            if server_type["id"] not in result:
                result[server_type["id"]] = ServerType(
                    name=server_type["id"],
                    price_hourly=server_type["monthly_cost"] / 625.0,
                    datacenters=[],
                    cores=server_type["vcpu_count"],
                    memory=server_type["ram"],
                    disk=server_type["disk"],
                    arch=OSArch.X64,
                )

            for datacenter in server_type["locations"]:
                result[server_type["id"]].datacenters.append(datacenter)

        return list(result.values())

    async def list_images(self) -> List[VMImage]:
        images_result = await self.get("/os")
        return [
            VMImage(
                image_id=os["id"],
                name=os["name"],
                arch=self._get_image_arch(os["arch"]),
                datacenter="all",
            )
            for os in images_result["os"]
        ]

    async def list_servers(self) -> List[BaseServer]:
        # XXX/TODO Implement pagination because this will easily return more results than 100-500.
        instances_result = await self.get("/instances")
        return [
            BaseServer(
                server_id=server["id"],
                server_name=server["label"] or server["id"],
                server_type=server["plan"],
                datacenter=server["region"],
                created=server["date_created"],
                ip4=server["main_ip"],
                ip6=server["v6_main_ip"] or None,
                server_tags=server["tag"].split(","),
            )
            for server in instances_result["instances"]
        ]

    async def _create_server(
        self,
        hostname: str = "",
        image: VMImage = "",
        server_type: ServerType = None,
        cloud_init: str = None,
        ssh_keys: List[SSHKey] = None,
        tags: List[str] = [],
        server_meta: dict = {},
        **kwargs,
    ):
        # Pick any region from the safe regions.
        safe_regions = await self._list_regions()
        picked_region = None
        for region in random.sample(
            server_type.datacenters, len(server_type.datacenters)
        ):
            if region in safe_regions:
                picked_region = region
                break
        if not picked_region:
            log.error(safe_regions)
            log.error(server_type.datacenters)
            raise Exception("Was unable to pick a region for " + server_type)

        # Deploy the server.
        create_data = dict(
            region=picked_region,
            plan=server_type.name,
            os_id=image.image_id,
            label=hostname,
            hostname=hostname,
            enable_ipv6=True,
            sshkey_id=[x.key_id for x in ssh_keys],
            user_data=to_b64(cloud_init),
            tag=",".join(tags),
            script_id=server_meta.get("startup_script"),
        )

        result = await self.post("/instances", data=create_data, json=True)

        return result

    async def delete_server(self, server: BaseServer):
        await self.delete("/instances/" + server.server_id)

    async def list_ssh_keys(self) -> List[SSHKey]:
        ssh_keys_result = await self.get("/ssh-keys")
        print(ssh_keys_result)
        return [
            SSHKey(
                name=key["name"],
                key=key["ssh_key"],
                key_id=key["id"],
            )
            for key in ssh_keys_result["ssh_keys"]
        ]

    # Internal methods
    async def _list_regions(self, country: Optional[str] = "US") -> List[str]:
        """Lists all region codes for Vultr.

        Country filter defaults to US because bandwidth overages is cheaper in the USA.

        Args:
            country (str, optional): The country to filter. Defaults to "US".
        """

        if "vultr_regions" not in self.BASE_CACHE:
            self.BASE_CACHE["vultr_regions"] = await self.get("/regions")

        return [
            region["id"]
            for region in self.BASE_CACHE["vultr_regions"]["regions"]
            if region["country"] == country
        ]

    def _get_image_arch(self, arch: str) -> OSArch:
        """Maps an image's arch string to an OSArch..

        Args:
            arch (str): An arch string

        Raises:
            ValueError: Unknown arch passed to this method.

        Returns:
            OSArch: The image's arch.
        """
        if arch == "x64":
            return OSArch.X64
        elif arch == "i386":
            return OSArch.X86

        raise ValueError("Unknown arch " + arch)
