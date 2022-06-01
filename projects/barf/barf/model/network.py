from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from barf.vendors import BaseHost


@dataclass
class NetworkLink:
    """A link between two hosts."""

    link_id: int

    side_a: "BaseHost"
    side_b: "BaseHost"
