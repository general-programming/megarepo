import asyncio
import dataclasses
import logging
import uuid
from typing import List
from urllib.parse import urljoin

import aiohttp
import aiolimiter
from zuscale.util import (
    OSArch,
    QuotaExceeded,
    ServerType,
    SSHKey,
    build_cloud_init,
    chunks,
    name_cleaner,
)

log = logging.getLogger("provider.base")


@dataclasses.dataclass
class VMImage:
    image_id: str
    name: str
    arch: OSArch
    datacenter: str

    def equals(self, other) -> bool:
        if isinstance(other, str):
            return self.clean_name == name_cleaner(other)

        if isinstance(other, VMImage):
            return self.image_id != other.image_id

    def __eq__(self, other) -> bool:
        return self.equals(other)

    @property
    def clean_name(self) -> str:
        return name_cleaner(self.name)


@dataclasses.dataclass
class BaseServer:
    server_id: str
    server_name: str
    server_type: str

    datacenter: str

    created: str

    ip4: str
    ip6: str = None

    data_out: int = 0

    # server_tags: List of tags set on the provider side.
    server_tags: List[str] = dataclasses.field(default_factory=list)

    @property
    def is_persistent(self):
        return "persistent" in self.server_tags


class BaseProvider:
    NAME = "base"
    BASE_DOMAIN = ""
    BASE_CACHE = {}

    # Requests / sec limit
    REQUESTS_LIMIT = 5

    # Base initialization logic
    def __init__(self, loop=None):
        if not loop:
            self.loop = asyncio.get_event_loop()

        self.loop = loop

        self.http = aiohttp.ClientSession(loop=self.loop, headers=self.headers)

        self.limiter = aiolimiter.AsyncLimiter(self.REQUESTS_LIMIT, 1)

    async def cleanup(self):
        await self.http.close()

    # HTTP client meat
    def _headers(self) -> dict:
        return {}

    # Pre response return hook for providers to do processing of results before doing anything with them.
    async def pre_hook(self, response):
        pass

    async def request(
        self, method: str, url: str, data=None, return_headers=False, **kwargs
    ):
        url = url.lstrip("/")
        api_url = urljoin(self.BASE_DOMAIN, url)
        headers = {}

        # Update headers to add other fields to it.
        headers.update(kwargs.pop("headers", {}))

        if kwargs.get("json", False):
            kwargs["json"] = data
        elif data:
            kwargs["data"] = data

        # Wait for the next request before shooting it off.
        await self.limiter.acquire()

        result = await self.http.request(
            method,
            api_url,
            headers=headers,
            **kwargs,
        )

        log.debug("%s:%s", result.status, api_url)

        # Return nothing if the status code is 204.
        if result.status == 204:
            if return_headers:
                return result.headers, None
            else:
                return None

        try:
            result_json = await result.json()
            await self.pre_hook(result_json)
        except Exception as e:
            print("Unknown error getting JSON.")
            print(await result.text())
            print(result.status)
            raise e

        if return_headers:
            return result.headers, result_json
        else:
            return result_json

    async def get(self, url, **kwargs):
        return await self.request("GET", url, **kwargs)

    async def delete(self, url, **kwargs):
        return await self.request("DELETE", url, **kwargs)

    async def post(self, url, **kwargs):
        return await self.request("POST", url, **kwargs)

    async def patch(self, url, **kwargs):
        return await self.request("PATCH", url, **kwargs)

    @property
    def headers(self) -> dict:
        result = {
            "User-Agent": "python-zuscale v0.0.1 alpha (contact nepeat@gmail, nepeat#0001@discord)"
        }
        result.update(self._headers())

        return result

    # Server type + pricing
    async def list_types(self) -> List[ServerType]:
        cache_key = f"{self.NAME}_server_types"

        if cache_key not in self.BASE_CACHE:
            self.BASE_CACHE[cache_key] = await self._list_types()

        return self.BASE_CACHE[cache_key]

    async def _list_types(self) -> List[ServerType]:
        raise NotImplementedError

    # Images
    async def list_images(self) -> List[VMImage]:
        raise NotImplementedError

    async def get_image(self, image_name: str) -> VMImage:
        for image in await self.list_images():
            if image.equals(image_name):
                return image

        raise Exception(f"Image {image_name} could not be found.")

    # Server management
    async def list_servers(self) -> List[BaseServer]:
        raise NotImplementedError

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
        raise NotImplementedError

    async def create_server(
        self,
        hostname: str = "",
        image: VMImage = "",
        server_types: List[ServerType] = "",
        cloud_init: str = None,
        ssh_keys: List[SSHKey] = None,
        tags: List[str] = [],
        server_meta: dict = {},
        **kwargs,
    ):
        if not hostname:
            hostname = "zuscale-" + str(uuid.uuid4())

        best_type = server_types[0]
        next_best = server_types[1] if len(server_types) > 1 else None

        try:
            return await self._create_server(
                hostname,
                image,
                best_type,
                cloud_init,
                ssh_keys,
                tags,
                server_meta,
                **kwargs,
            )
        except (QuotaExceeded, IndexError):
            if len(server_types) > 1:
                log.debug(
                    "Hit the quota for %s nodes, now deploying %s nodes.",
                    best_type.name,
                    next_best.name,
                )
            else:
                raise QuotaExceeded("Out of eligible server types to fallback to.")

            if server_types and server_types[0] == best_type:
                server_types.remove(best_type)

            return await self.create_server(
                hostname,
                image,
                server_types,
                cloud_init,
                ssh_keys,
                tags,
                server_meta,
                **kwargs,
            )

    async def delete_server(self, server: BaseServer):
        raise NotImplementedError

    async def delete_all_servers(self, sleep=1, safe=True):
        # XXX/TODO: do something with sleep again.
        tasks = []

        for server in await self.list_servers():
            if safe and server.is_persistent:
                log.info(
                    "Refusing to delete server %s due to persistent tag.",
                    server.server_name,
                )
                continue

            log.info("Deleting server %s.", server.server_name)
            tasks.append(self.delete_server(server))

        await asyncio.gather(*tasks)

    async def get_server_type(self, type_name: str) -> ServerType:
        if type_name in self.BASE_CACHE:
            return self.BASE_CACHE[type_name]

        for server_type in await self.list_types():
            if server_type == type_name:
                self.BASE_CACHE[type_name] = server_type
                return server_type

        # Return nothing if the server type could not be found.
        raise Exception(f"Server type {type_name} could not be found.")

    async def pick_server_type(self, specs: dict):
        eligible = []
        min_ram = specs.get("ram", 0)
        min_cpu = specs.get("cpu", 0)
        price = specs.get("price", None)

        for server_type in await self.list_types():
            # Basic memory + CPU limitations.
            if server_type.memory < min_ram:
                continue

            if server_type.cores < min_cpu:
                continue

            # For now, let's launch only x86 based servers.
            if server_type.arch != OSArch.X64:
                continue

            # Do limits by maximum price if given.
            if price and server_type.price_hourly > price:
                continue

            eligible.append(server_type)

        # Sort by price,
        eligible = sorted(eligible, key=lambda x: x.price_hourly)

        # Return the cheapest server.
        return eligible

    async def deploy_server(self, config: dict):
        # Get the base configs that we need.
        server_specs = config["specs"]
        server_provider_info = config["providers"][self.NAME]
        server_meta = server_provider_info.get("meta", {})
        launch_amount = int(server_provider_info.get("amount", 1))

        # Stop working if the amount is 0.
        if launch_amount == 0:
            return

        # Get the server types as an explicit option or as an implicit one based on specs.
        server_types = []

        if "server_type" in config:
            server_types.append(await self.get_server_type(config["server_type"]))
        else:
            server_types.extend(await self.pick_server_type(server_specs))

        # Read the cloud-config from the config or from a file, depending on the format.
        cloud_init = config.get("cloud_init", "")
        if cloud_init.endswith(".yml"):
            with open(cloud_init, "r") as f:
                cloud_init = f.read()
        elif cloud_init == "builder":
            cloud_init_template = server_provider_info.get(
                "cloud_init_template", config["cloud_init_template"]
            )
            cloud_init = build_cloud_init(cloud_init_template)

        # Get the VM image from the provider.
        vm_image = await self.get_image(server_provider_info["image"])

        # Read our provider's SSH keys and save them for the VM.
        ssh_keys = await self.list_ssh_keys()

        # Generate a list of tags based on some metadata.
        tags = []
        project_name = config.get("project")
        config_tags = config.get("tags", [])

        if project_name:
            tags.append("project_" + project_name)

        tags.extend(config_tags)

        # Do the creation of the servers.
        tasks = []

        for x in range(0, launch_amount):
            tasks.append(
                self.create_server(
                    hostname=config.get("hostname", ""),
                    image=vm_image,
                    server_types=server_types,
                    cloud_init=cloud_init,
                    ssh_keys=ssh_keys,
                    tags=tags,
                    server_meta=server_meta,
                )
            )

        await asyncio.gather(*tasks)

    # SSH key management
    async def list_ssh_keys(self) -> List[SSHKey]:
        raise NotImplementedError

    async def add_ssh_key(self, ssh_key: str) -> bool:
        raise NotImplementedError

    async def delete_ssh_key(self, ssh_key: str) -> bool:
        raise NotImplementedError
