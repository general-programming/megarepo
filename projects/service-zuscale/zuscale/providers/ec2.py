import logging
from typing import List

import aiobotocore

from zuscale.providers.base import BaseProvider, BaseServer, OSArch, VMImage
from zuscale.util import ServerType, SSHKey, get_env

log = logging.getLogger("provider.ec2")


class Ec2ServerType(ServerType):
    region: str


class Ec2(BaseProvider):
    NAME = "ec2"

    def __init__(self, loop=None):
        self.access_key = get_env("EC2_ACCESS_KEY")
        self.secret_key = get_env("EC2_SECRET_KEY")

        self.regions = get_env("EC2_REGIONS", "us-east-1").split(",")

        self.session = aiobotocore.get_session()

        super().__init__(loop)

    # async def get_server_type(self, type_name: str) -> Ec2ServerType:

    async def _list_types(self) -> List[Ec2ServerType]:
        instances = []

        async with self.session.create_client(
            "ec2",
            region_name="us-east-1",
            aws_secret_access_key=self.secret_key,
            aws_access_key_id=self.access_key,
        ) as client:
            extra_args = {}

            while True:
                instance_types_response = await client.describe_instance_types(
                    **extra_args
                )

                instances.extend(
                    Ec2ServerType(
                        name=instance_type["InstanceType"],
                        # XXX Fuck. https://boto3.amazonaws.com/v1/documentation/api/latest/reference/services/pricing.html
                        price_hourly=0.00,
                        # XXX Shit!
                        datacenters=[],
                        cores=instance_type["VCpuInfo"]["DefaultVCpus"],
                        memory=instance_type["MemoryInfo"]["SizeInMiB"],
                        # XXX oh no
                        disk=0.0,
                        # XXX lol gravitron
                        arch=OSArch.X64,
                    )
                    for instance_type in instance_types_response["InstanceTypes"]
                )

                if "NextToken" in instance_types_response:
                    extra_args["NextToken"] = instance_types_response["NextToken"]
                else:
                    break

        return instances

    async def list_servers(self) -> List[BaseServer]:
        instances = []

        for region in self.regions:
            async with self.session.create_client(
                "ec2",
                region_name=region,
                aws_secret_access_key=self.secret_key,
                aws_access_key_id=self.access_key,
            ) as client:
                extra_args = {}

                while True:
                    instances_response = await client.describe_instances()

                    for response in instances_response["Reservations"]:
                        for instance in response["Instances"]:
                            if (
                                instance.get("State", {}).get("Name", "")
                                == "terminated"
                            ):
                                continue

                            instances.append(
                                BaseServer(
                                    server_id=instance["InstanceId"],
                                    server_name=instance["InstanceId"],
                                    server_type=instance["InstanceType"],
                                    server_tags=[
                                        x["Value"] for x in instance.get("Tags", [])
                                    ],
                                    datacenter=region,
                                    created=instance["LaunchTime"],
                                    ip4=instance["PublicIpAddress"],
                                    ip6=None,
                                )
                            )

                    if "NextToken" in instances_response:
                        extra_args["NextToken"] = instances_response["NextToken"]
                    else:
                        break

            return instances

    async def list_images(self) -> List[VMImage]:
        raise NotImplementedError

    # async def list_servers(self) -> List[BaseServer]:
    #     servers = []

    #     for region in self.regions:
    #         async with self.session.create_client(
    #             "ec2", region_name=region,
    #             aws_secret_access_key=self.secret_key,
    #             aws_access_key_id=self.access_key
    #         ) as client:
    #             instances_response = await client.describe_instances()
    #             for response in instances_response["Reservations"]:
    #                 for instance in response["Instances"]:
    #                     if instance.get("State", {}).get("Name", "") == "terminated":
    #                         continue

    #                     servers.append(BaseServer(
    #                         server_id=instance["InstanceId"],
    #                         server_name=instance["InstanceId"],
    #                         server_type=instance["InstanceType"],
    #                         server_tags=[x["Value"] for x in instance.get("Tags", [])],
    #                         datacenter=region,
    #                         created=instance["LaunchTime"],
    #                         ip4=instance["PublicIpAddress"],
    #                         ip6=None,
    #                     ))

    #     return servers

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

    async def delete_server(self, server: BaseServer):
        raise NotImplementedError

    async def list_ssh_keys(self) -> List[SSHKey]:
        raise NotImplementedError
