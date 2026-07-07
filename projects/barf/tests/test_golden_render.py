"""Golden-parity harness for the config-blocks refactor.

Renders every templatable network.yml host with deterministic fake
secrets and compares the output byte-for-byte against a frozen golden
file. The template -> ConfigBlock migration must keep every golden
identical; regenerate deliberately with::

    pytest tests/test_golden_render.py --update-goldens

Goldens contain only fake secret values (SECRET-*/VAULT-*/PUB-*/PRIV-*),
never real ones.
"""

import pathlib

import pytest
import yaml

from barf.util.network import load_network
from barf.util.render import render_host_config
from barf.vendors import VENDOR_MAP, BaseHost

pytestmark = pytest.mark.usefixtures("fake_keys", "fake_vault")

BARF_ROOT = pathlib.Path(__file__).parent.parent
NETWORK_YML = BARF_ROOT / "network.yml"
GOLDEN_DIR = pathlib.Path(__file__).parent / "golden"


def _templatable_hostnames() -> list[str]:
    """network.yml hostnames whose vendor renders a config.

    Reads the raw YAML (no host construction) so collection never
    touches Vault plumbing.
    """
    raw = yaml.safe_load(NETWORK_YML.read_text())
    return [
        name
        for name, meta in raw["hosts"].items()
        if VENDOR_MAP[meta["type"]].TEMPLATABLE
    ]


class FakeSecrets:
    """Deterministic stand-in for util.secrets.VaultSecrets."""

    def __getattr__(self, key: str) -> str:
        return f"VAULT-{key}"

    def __getitem__(self, key: str) -> str:
        return f"VAULT-{key}"


@pytest.fixture
def fleet(monkeypatch):
    """The real network.yml fleet with deterministic host secrets."""
    monkeypatch.setattr(
        BaseHost,
        "secret",
        lambda self, key, default_value=None, secret_path=None: (
            f"SECRET-{secret_path or 'host-' + self.hostname}-{key}"
        ),
    )
    return load_network(str(NETWORK_YML))


def _render(fleet, hostname: str) -> str:
    hosts, links, global_meta = fleet
    host = next(h for h in hosts if h.hostname == hostname)
    return render_host_config(host, links, global_meta, FakeSecrets())


@pytest.mark.parametrize("hostname", _templatable_hostnames())
def test_render_matches_golden(hostname, fleet, request):
    rendered = _render(fleet, hostname)
    golden = GOLDEN_DIR / f"{hostname}.conf"

    if request.config.getoption("--update-goldens"):
        GOLDEN_DIR.mkdir(exist_ok=True)
        golden.write_text(rendered)
        return

    assert golden.exists(), (
        f"missing golden for {hostname}; run"
        " pytest tests/test_golden_render.py --update-goldens"
    )
    assert rendered == golden.read_text(), (
        f"{hostname} render drifted from tests/golden/{hostname}.conf;"
        " a parity port must be byte-identical (regenerate the goldens"
        " only for a deliberate output change, in its own commit)"
    )


@pytest.mark.parametrize("hostname", _templatable_hostnames())
def test_render_is_deterministic(hostname, fleet):
    """Two renders of the same host are identical.

    Guards the golden contract itself: any nondeterminism (set
    ordering, generated values sneaking past the stubs) would make
    byte-parity meaningless.
    """
    assert _render(fleet, hostname) == _render(fleet, hostname)
