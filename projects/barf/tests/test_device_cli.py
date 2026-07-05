import importlib
from types import SimpleNamespace

import click
import pytest

from barf.cli.device import wait_for_device_alive

# "from barf.cli import device" would give the click group of the same
# name, not the module.
device_cli = importlib.import_module("barf.cli.device")


def test_wait_for_device_alive_waits_then_polls(monkeypatch):
    sleeps = []
    monkeypatch.setattr(device_cli.time, "sleep", sleeps.append)
    monkeypatch.setattr(device_cli.time, "monotonic", lambda: float(len(sleeps)))

    versions = iter([None, None, "1.5-rolling"])
    host = SimpleNamespace(hostname="testbox", version=lambda: next(versions))

    assert wait_for_device_alive(host) == "1.5-rolling"
    # 15s head start, then 5s between the two failed polls.
    assert sleeps == [15, 5, 5]


def test_wait_for_device_alive_times_out(monkeypatch):
    monkeypatch.setattr(device_cli.time, "sleep", lambda s: None)
    clock = iter(range(0, 10_000, 500))
    monkeypatch.setattr(device_cli.time, "monotonic", lambda: float(next(clock)))

    host = SimpleNamespace(hostname="testbox", version=lambda: None)
    with pytest.raises(click.ClickException, match="did not come back"):
        wait_for_device_alive(host, timeout=900)
