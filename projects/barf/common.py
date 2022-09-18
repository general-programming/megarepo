import ipaddress
import os
from dataclasses import dataclass

from gql import Client
from gql.transport.aiohttp import AIOHTTPTransport


def get_nb_client() -> Client:
    # Select your transport with a defined url endpoint
    transport = AIOHTTPTransport(
        url="https://netbox.generalprogramming.org/graphql/",
        headers={"Authorization": f"Token {os.environ['NETBOX_API_KEY']}"},
    )

    # Create a GraphQL client using the defined transport
    return Client(transport=transport, fetch_schema_from_transport=True)


def is_internal(address):
    return ipaddress.ip_network(address, strict=False).is_private


def clean_hostname(data: str) -> str:
    return data.replace(" ", "_").replace(":", "").replace("_-_", "_").replace("/", "_")


@dataclass
class IPAMHost:
    hostname: str
    ipv4: str = None
    ipv6: str = None
    mac: str = None
    ipmi_ip: str = None

    @property
    def clean_hostname(self):
        return clean_hostname(self.hostname)
