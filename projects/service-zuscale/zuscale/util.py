"""General shared functions and classes by the whole app."""

import base64
import os
from dataclasses import dataclass
from enum import Enum
from io import StringIO
from typing import List

import hvac
import yaml


def name_cleaner(name: str) -> str:
    """Clean a name by replacing symbols with other symbols.

    Args:
        name (str): The name to clean.

    Returns:
        str: The cleaned name.
    """
    return name.replace("-", "_").replace(" ", "_").lower()


class OSArch(Enum):
    """Represents the architectures given by providers."""

    X86 = 1
    X64 = 2
    ARM64 = 3
    OTHER = 4


@dataclass
class ServerType:
    """A type of server that can be launched by a provider."""

    name: str
    datacenters: List[str]

    # Hourly Price: Units dependent on account.
    price_hourly: float

    # Cores: Units per core.
    cores: int

    # Memory: Units in GB.
    memory: int

    # Disk: Units in GB.
    disk: int

    # Arch: Any valid OSArch.
    arch: OSArch

    def __eq__(self, other) -> bool:
        """Compare the current ServerType to other ServerType or names.

        Args:
            other (ServerType, str): Another ServerType or a name to compare to.

        Returns:
            bool: The equality of both objects.
        """
        if isinstance(other, ServerType):
            return self.clean_name == other.clean_name

        return self.clean_name == name_cleaner(other)

    @property
    def clean_name(self) -> str:
        """Clean name of the current ServerType.

        Returns:
            str: The cleaned name of the server type.
        """
        return name_cleaner(self.name)


@dataclass
class SSHKey:
    """An SSH key as stored by a provider."""

    name: str
    key: str

    # Optional key ID, used by some providers.
    key_id: str = None


class QuotaExceeded(Exception):
    """A quota exceeded error given by a provider's API."""

    pass


class BackendError(Exception):
    """An error happened in the backend of a provider."""

    pass


def get_env(key: str, default: str = None) -> str:
    """Get a variable from the environment and returns it.

    Args:
        key (str): The key to get from the environment.

    Raises:
        EnvironmentError: Thrown when the variable is not set in the environment.

    Returns:
        str: The value of the key in the environment.
    """
    if key not in os.environ and not default:
        raise EnvironmentError(f"{key} is not set in the environment.")

    return os.environ.get(key, default)


def chunks(lst: List[any], n: int):
    """Yield successive n-sized chunks from lst."""
    for i in range(0, len(lst), n):
        yield lst[i : i + n]


def to_b64(data: any) -> str:
    """Converts any form of data into a base64 string.

    Args:
        data (any): Data goes in.

    Returns:
        str: Base64 encoded data comes out.
    """
    if isinstance(data, (int, float)):
        data = str(data)

    if isinstance(data, str):
        data = data.encode("utf8")

    return base64.b64encode(data).decode("utf8")


def get_hvac() -> hvac.Client:
    client = hvac.Client()
    client.secrets.kv.default_kv_version = 2

    if not client.is_authenticated():
        raise Exception("'vault login' is required to deploy.")

    return client


def get_from_vault(client: hvac.Client, key: str) -> str:
    response = client.secrets.kv.v2.read_secret_version(
        path=key,
        mount_point="webscale-scrape",
    )["data"]["data"]["file"]

    return response


def build_cloud_init(template_name: str) -> str:
    # Open the template YAML and load it.
    with open(template_name, "r") as f:
        output = yaml.load(f)

    # Get the base dir of the template.
    template_base_dir = os.path.dirname(os.path.abspath(template_name))

    # File building, the nasty bits.
    files: List[dict] = []

    def add_file(filename, content, file=False, **kwargs):
        data = {
            "path": filename,
            "content": content,
        }

        # Read the file if it is a file.
        if file:
            full_path = os.path.join(template_base_dir, content)
            with open(full_path, "rb") as f:
                data["content"] = f.read()

        # Encode content as base64
        if isinstance(data["content"], str):
            data["content"] = data["content"].encode("utf8")

        data["encoding"] = "b64"
        data["content"] = base64.b64encode(data["content"]).decode("utf8")

        # Add other args.
        data.update(kwargs)

        # Add the file.
        files.append(data)

    # Add cluster approle
    hvac_client = get_hvac()

    # Add AppRole ID + secret.
    approle_name = "cluster-node"
    approle_id = hvac_client.auth.approle.read_role_id(role_name=approle_name)["data"][
        "role_id"
    ]
    approle_secret = hvac_client.write(
        path=f"auth/approle/role/{approle_name}/secret-id"
    )["data"]["secret_id"]
    add_file("/opt/vault/approle_name", approle_name, permissions="0644")
    add_file("/opt/vault/approle_id", approle_id, permissions="0644")
    add_file("/opt/vault/approle_secret", approle_secret, permissions="0644")

    # Build the group data file.
    add_file(
        "/tmp/pyinfra_all.py",
        get_from_vault(hvac_client, "pyinfra_all"),
        permissions="0644",
    )

    # Add the other files to the template.
    for file_item in output.pop("static_files", []):
        add_file(
            file_item["dest"],
            file_item["source"],
            file=True,
            permissions=file_item.get("permissions", "0644"),
        )

    # Add the files to the template.
    if "write_files" not in output and files:
        output["write_files"] = []

    output["write_files"].extend(files)

    # Create the actual cloud-init.
    outf = StringIO()
    outf.write("#cloud-config\n")
    yaml.dump(output, outf)

    return outf.getvalue()
