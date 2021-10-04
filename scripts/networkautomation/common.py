import os
import ipaddress

from gql import Client
from gql.transport.aiohttp import AIOHTTPTransport


def get_nb_client() -> Client:
    # Select your transport with a defined url endpoint
    transport = AIOHTTPTransport(
        url="https://netbox.generalprogramming.org/graphql/",
        headers={
            "Authorization": f"Token {os.environ['NETBOX_KEY']}"
        },
    )

    # Create a GraphQL client using the defined transport
    return Client(
        transport=transport,
        fetch_schema_from_transport=True
    )


def is_internal(address):
    return ipaddress.ip_network(address, strict=False).is_private


def clean_hostname(data: str) -> str:
    return data.replace(" ", "_").replace(":", "").replace("_-_", "_").replace("/", "_")
