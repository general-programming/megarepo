import logging
from dataclasses import dataclass
from typing import List

from zuscale.providers.base import BaseProvider, BaseServer, OSArch, VMImage
from zuscale.util import BackendError, QuotaExceeded, ServerType, SSHKey, get_env

log = logging.getLogger("provider.scaleway")


@dataclass
class ScalewayImage(VMImage):
    server_types: List[str]


class Scaleway(BaseProvider):
    NAME = "scaleway"
    BASE_DOMAIN = "https://api.scaleway.com/instance/v1/zones/"
    REQUESTS_LIMIT = 4

    ZONES = ["fr-par-1", "fr-par-2", "nl-ams-1", "pl-waw-1"]

    def __init__(self, loop=None):
        self.token = get_env("SCALEWAY_TOKEN")
        self.scw_project_id = get_env("SCALEWAY_PROJECT")

        super().__init__(loop)

    def _headers(self) -> dict:
        return {"X-Auth-Token": self.token}

    @staticmethod
    def _check_error(error: dict, response: dict = None):
        if not error:
            error = {}

        error_message = error.get("message", "").lower()

        if "quota exceeded for this resource" in error_message:
            raise QuotaExceeded()

        if "internal error" == error_message:
            raise BackendError()

    async def pre_hook(self, response):
        if not response:
            response = {}

        self._check_error(response, response)

    # XXX Implement this later using the undocumented Go API.
    # https://github.com/scaleway/scaleway-sdk-go/blob/2a50b0604db3dd9e132f79d395ff8aa3f16c4a27/api/account/v2alpha1/account_sdk.go
    async def list_ssh_keys(self) -> List[dict]:
        return []

    async def list_images(self) -> List[ScalewayImage]:
        result = []

        # images = await self._get_marketplace_images()
        local_images = await self._get_images()

        # Snapshot images
        for image in local_images:
            zone = image["zone"].replace("par1", "fr-par-1").replace("ams1", "nl-ams-1")
            result.append(
                ScalewayImage(
                    image_id=image["id"],
                    name=image["name"],
                    arch=self._scw_get_arch(image["arch"]),
                    datacenter=zone,
                    # XXX: Assume any server type, what can go wrong?
                    server_types=["any"],
                )
            )

        return result

    async def list_servers(self) -> List[BaseServer]:
        result = []

        for zone in self.ZONES:
            async for server in self._paginate_get("/" + zone + "/servers", "servers"):
                result.append(
                    BaseServer(
                        server_id=str(server["id"]),
                        server_name=server["name"],
                        server_type=server["commercial_type"],
                        server_tags=server["tags"],
                        datacenter=zone,
                        created=server["creation_date"],
                        ip4=server["public_ip"]["address"]
                        if server["public_ip"]
                        else None,
                        ip6=server["ipv6"]["address"] if server["ipv6"] else None,
                    )
                )

        return result

    async def _list_types(self) -> List[ServerType]:
        # We store the result as a dict and return it as a list so we can update the datacenters per zone.
        result = {}

        for zone in self.ZONES:
            types_result = await self.get("/" + zone + "/products/servers")
            for server_type, server_info in types_result["servers"].items():
                # Create the instance type in the result with a blank datacenters list to append to later.
                if server_type not in result:
                    result[server_type] = ServerType(
                        name=server_type,
                        price_hourly=server_info["hourly_price"],
                        datacenters=[],
                        cores=server_info["ncpus"],
                        memory=server_info["ram"] / 1024 / 1024,
                        disk=server_info["volumes_constraint"]["min_size"]
                        / 1000
                        / 1000
                        / 1000,
                        arch=self._scw_get_arch(server_info["arch"]),
                    )

                # Add our zone to the list of datacenters.
                result[server_type].datacenters.append(zone)

        return list(result.values())

    async def delete_server(self, server: BaseServer):
        log.debug("deleted server %s", server)
        await self.post(
            "/" + server.datacenter + "/servers/" + server.server_id + "/action",
            data={"action": "terminate"},
            json=True,
        )
        # await self.delete("/" + server.datacenter + "/servers/" + server.server_id)

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
        zone = self.ZONES[0]
        image = await self._get_marketplace_image(zone, image, server_type)

        try:
            result = await self.post(
                "/" + zone + "/servers",
                data={
                    "name": hostname,
                    "commercial_type": server_type.name,
                    "image": image.image_id,
                    "project": self.scw_project_id,
                    # Seriously?
                    "enable_ipv6": True,
                    "tags": tags,
                    **kwargs,
                },
                json=True,
            )
        except BackendError:
            return await self._create_server(
                hostname,
                image,
                server_type,
                cloud_init,
                ssh_keys,
                tags,
                server_meta,
                **kwargs,
            )

        log.debug(result)

        new_server_id = result["server"]["id"]

        # This returns a 204, nothing interesting to save here.
        await self.patch(
            "/" + zone + "/servers/" + new_server_id + "/user_data/cloud-init",
            data=cloud_init,
            headers={
                # oh come on,,,
                # Content-Type must be text/plain and not text/plain; charset=utf-8
                "Content-Type": "text/plain"
            },
        )

        await self.post(
            "/" + zone + "/servers/" + new_server_id + "/action",
            data={"action": "poweron"},
            json=True,
        )

        return result

    # Internal functions
    async def _get_images(self):
        if "scaleway_local_images" not in self.BASE_CACHE:
            result = []

            for zone in self.ZONES:
                images = await self.get("/" + zone + "/images")
                result.extend(images["images"])

            self.BASE_CACHE["scaleway_local_images"] = result

        return self.BASE_CACHE["scaleway_local_images"]

    async def _get_marketplace_images(self):
        if "scaleway_marketplace_images" not in self.BASE_CACHE:
            images_request = await self.http.get(
                "https://api-marketplace.scaleway.com/images"
            )
            self.BASE_CACHE["scaleway_marketplace_images"] = await images_request.json()

        return self.BASE_CACHE["scaleway_marketplace_images"]

    async def _get_marketplace_image(
        self, zone: str, base_image: ScalewayImage, server_type: str
    ) -> ScalewayImage:
        images = await self.list_images()
        for image in images:
            # Skip over images that are not in our zone.
            if image.datacenter != zone:
                continue

            # Skip images where the supported types does not include our server type.
            if (
                server_type not in image.server_types
                and "any" not in image.server_types
            ):
                continue

            if image.name == base_image.name:
                return image

        raise IndexError(f"Image {base_image.name} for zone {zone} cannot be found.")

    @staticmethod
    def _scw_get_arch(arch: str):
        if arch == "x86_64":
            return OSArch.X64
        elif arch in ("arm", "arm64"):
            return OSArch.ARM64
        else:
            raise Exception("Unknown arch " + arch)

    # XXX/TODO: Duplication of pagination functions, can it be merged?
    async def _paginate_get(self, endpoint, field: str = None):
        complete = False
        page = 1

        while not complete:
            log.debug("fetching page %d", page)
            headers, result = await self.get(
                endpoint, params={"page": page, "per_page": 100}, return_headers=True
            )

            if field:
                # Yield whole pages if we do not have a field set.
                for item in result[field]:
                    yield item
            else:
                # Yield whole pages if we do not have a field set.
                yield result

            # Pull the pagination data based on X-Total-Count.
            total_count = int(headers.get("x-total-count", 0))
            if total_count / (100 * page) < 1:
                complete = True

            # Add 1 to the page.
            page += 1
