import logging
from typing import List

from zuscale.providers.base import BaseProvider, BaseServer, OSArch, VMImage
from zuscale.util import ServerType, SSHKey, get_env

log = logging.getLogger("provider.hetzner_cloud")


class HetznerCloud(BaseProvider):
    NAME = "hetzner_cloud"
    BASE_DOMAIN = "https://api.hetzner.cloud/v1/"
    REQUESTS_LIMIT = 20

    def __init__(self, loop=None):
        self.token = get_env("HETZNER_TOKEN")

        super().__init__(loop)

    def _headers(self) -> dict:
        return {
            "Authorization": "Bearer " + self.token
        }

    @staticmethod
    def _check_error(error: dict, response: dict = None):
        if not error:
            error = {}

        error_code = error.get("code", None)
        error_message = error.get("message", None)

        if error_code:
            log.error(response)
            raise Exception(f"Hetzner error '{error_code}': {error_message}")

    async def pre_hook(self, response):
        self._check_error(response.get("error", {}), response)
        self._check_error(response.get("action", {}).get("error", {}), response)

    async def list_servers(self):
        return [
            BaseServer(
                server_id=str(server["id"]),
                server_name=server["name"],
                server_type=server["server_type"]["name"],
                server_tags=[x for x in server["labels"].values()],
                datacenter=server["datacenter"]["name"],
                created=server["created"],
                ip4=server["public_net"]["ipv4"]["ip"],
                ip6=server["public_net"]["ipv6"]["ip"],
                data_out=server["outgoing_traffic"] or 0,
            )
            async for server in self._paginate_get("/servers", "servers")
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
        **kwargs
    ):
        # Add the SSH keys if they exist.
        if ssh_keys:
            kwargs["ssh_keys"] = [x.name for x in ssh_keys]

        # Add the tags into labels if they exist.
        labels = {"tag_" + str(i): tag for i, tag in enumerate(tags)}

        # Get server datacenter from meta.
        location = server_meta.get("region", "ash")

        # Add networks.
        if "network" in server_meta:
            kwargs["networks"] = [server_meta["network"]]

        result = await self.post("/servers", data={
            "name": hostname,
            "location": location,
            "server_type": server_type.name,
            "image": image.image_id,
            "user_data": cloud_init,
            "labels": labels,
            **kwargs
        }, json=True)

        log.debug(result)

        return result

    async def delete_server(self, server: BaseServer):
        log.debug("deleted server %s", server)
        await self.delete("servers/" + server.server_id)

    async def list_ssh_keys(self) -> List[SSHKey]:
        keys = await self.get("/ssh_keys")

        return [
            SSHKey(
                name=key["name"],
                key=key["public_key"],
            )
            for key in keys["ssh_keys"]
        ]

    async def list_images(self) -> List[VMImage]:
        images_result = await self.get("/images")
        # print(__import__("json").dumps(images_result, indent=2))

        return [
            VMImage(
                image_id=image["id"],
                name=image["name"] or image["description"],
                arch=OSArch.X64,
                datacenter="hcloud",
            )
            for image in images_result["images"]
        ]

    async def _list_types(self) -> List[ServerType]:
        server_types_result = await self.get("/server_types")

        return [
            ServerType(
                name=server_type["name"],
                price_hourly=float(server_type["prices"][0]["price_hourly"]["net"]),
                datacenters=["hcloud"],  # Hetzner has the same prices for all DCs.
                cores=server_type["cores"],
                memory=server_type["memory"] * 1024,
                disk=server_type["disk"],
                arch=OSArch.X64,
            )
            for server_type in server_types_result["server_types"]
        ]

    # Internal use functions
    # XXX/TODO: Duplication of pagination functions, can it be merged?
    async def _paginate_get(self, endpoint, field: str = None):
        complete = False
        page = 1

        while not complete:
            log.debug("fetching page %d", page)
            result = await self.get(endpoint, params={
                "page": page,
                "per_page": 50
            })

            if field:
                # Yield whole pages if we do not have a field set.
                for item in result[field]:
                    yield item
            else:
                # Yield whole pages if we do not have a field set.
                yield result

            # Parse the result, pull the pagination data.
            next_page = result.get("meta", {}).get("pagination", {}).get("next_page", None)
            if next_page:
                page = next_page
            else:
                complete = True
