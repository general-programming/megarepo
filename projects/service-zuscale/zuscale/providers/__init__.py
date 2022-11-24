import os

from typing import Dict

from zuscale.providers.base import BaseProvider
from zuscale.providers.hetzner_cloud import HetznerCloud
from zuscale.providers.scaleway import Scaleway
from zuscale.providers.vultr import Vultr
from zuscale.providers.ec2 import Ec2

ALL_CLOUDS = {} # type: Dict[str, BaseProvider]

if os.environ.get("HETZNER_TOKEN", ""):
    ALL_CLOUDS["hetzner_cloud"] = HetznerCloud

if os.environ.get("SCALEWAY_TOKEN", ""):
    ALL_CLOUDS["scaleway"] = Scaleway

if os.environ.get("VULTR_TOKEN", ""):
    ALL_CLOUDS["vultr"] = Vultr

if os.environ.get("EC2_ACCESS_KEY", ""):
    ALL_CLOUDS["ec2"] = Ec2

__all__ = [*ALL_CLOUDS.values(), ALL_CLOUDS]
