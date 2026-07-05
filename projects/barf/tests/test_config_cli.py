import importlib

from click.testing import CliRunner

from barf.cli.config import config
from barf.vendors import DeployDiff

# "from barf.cli import config" would give the click group of the same
# name, not the module.
config_cli = importlib.import_module("barf.cli.config")


class FakeHost:
    def __init__(
        self,
        hostname: str,
        diff: DeployDiff = None,
        diff_error: Exception = None,
        templatable: bool = True,
    ):
        self.hostname = hostname
        self.is_templatable = templatable
        self._diff = diff
        self._diff_error = diff_error
        self.pushed = []

    def diff_config(self, rendered: str, *, redact=True, show_device_only=False):
        if self._diff_error is not None:
            raise self._diff_error
        return self._diff

    def push_rendered_config(self, rendered: str) -> None:
        self.pushed.append(rendered)


def wire(monkeypatch, tmp_path, hosts):
    """Point the config CLI at fake hosts and a no-op renderer."""
    network = tmp_path / "network.yml"
    network.write_text("hosts: {}\n")

    monkeypatch.setattr(config_cli, "load_network", lambda filename: (hosts, [], {}))
    monkeypatch.setattr(config_cli, "_secrets", lambda: {})
    monkeypatch.setattr(
        config_cli,
        "render_host_config",
        lambda host, links, global_meta, secrets: f"set rendered {host.hostname}\n",
    )
    return str(network)


def test_generate_writes_output(monkeypatch, tmp_path):
    host = FakeHost("box1")
    filename = wire(monkeypatch, tmp_path, [host])

    written = []
    monkeypatch.setattr(
        config_cli,
        "write_rendered_config",
        lambda host, rendered: (
            written.append((host.hostname, rendered)) or f"output/{host.hostname}"
        ),
    )

    result = CliRunner().invoke(config, ["generate", "all", "--filename", filename])
    assert result.exit_code == 0, result.output
    assert written == [("box1", "set rendered box1\n")]
    assert "wrote output/box1" in result.output


def test_unknown_target_fails(monkeypatch, tmp_path):
    filename = wire(monkeypatch, tmp_path, [FakeHost("box1")])
    result = CliRunner().invoke(config, ["diff", "nosuchbox", "--filename", filename])
    assert result.exit_code != 0
    assert "unknown device" in result.output


def test_diff_prints_diff_and_summary(monkeypatch, tmp_path):
    host = FakeHost(
        "box1",
        diff=DeployDiff(
            text="+ set system host-name new", has_changes=True, summary="+1"
        ),
    )
    filename = wire(monkeypatch, tmp_path, [host])

    result = CliRunner().invoke(config, ["diff", "all", "--filename", filename])
    assert result.exit_code == 0, result.output
    assert "+ set system host-name new" in result.output
    assert "+1" in result.output


def test_diff_skips_unsupported_vendors(monkeypatch, tmp_path):
    host = FakeHost("box1", diff_error=NotImplementedError("nope"))
    filename = wire(monkeypatch, tmp_path, [host])

    result = CliRunner().invoke(config, ["diff", "all", "--filename", filename])
    assert result.exit_code == 0, result.output
    assert "skipped: no diff support" in result.output


def test_diff_reports_failures_and_exits_nonzero(monkeypatch, tmp_path):
    hosts = [
        FakeHost("bad", diff_error=RuntimeError("boom")),
        FakeHost(
            "good", diff=DeployDiff(text="", has_changes=False, summary="no changes")
        ),
    ]
    filename = wire(monkeypatch, tmp_path, hosts)

    result = CliRunner().invoke(config, ["diff", "all", "--filename", filename])
    assert result.exit_code == 1
    assert "failed: boom" in result.output
    # A failing device must not stop the rest of the fleet from diffing.
    assert "no changes" in result.output


def test_deploy_skips_when_no_changes(monkeypatch, tmp_path):
    host = FakeHost(
        "box1", diff=DeployDiff(text="", has_changes=False, summary="no changes")
    )
    filename = wire(monkeypatch, tmp_path, [host])

    result = CliRunner().invoke(
        config, ["deploy", "all", "--yes", "--filename", filename]
    )
    assert result.exit_code == 0, result.output
    assert host.pushed == []
    assert "no changes" in result.output


def test_deploy_pushes_with_yes(monkeypatch, tmp_path):
    host = FakeHost(
        "box1", diff=DeployDiff(text="+ set x", has_changes=True, summary="+1")
    )
    filename = wire(monkeypatch, tmp_path, [host])

    result = CliRunner().invoke(
        config, ["deploy", "all", "--yes", "--filename", filename]
    )
    assert result.exit_code == 0, result.output
    assert host.pushed == ["set rendered box1\n"]
    assert "deployed (+1)" in result.output


def test_deploy_declined_pushes_nothing(monkeypatch, tmp_path):
    host = FakeHost(
        "box1", diff=DeployDiff(text="+ set x", has_changes=True, summary="+1")
    )
    filename = wire(monkeypatch, tmp_path, [host])

    result = CliRunner().invoke(
        config, ["deploy", "all", "--filename", filename], input="n\n"
    )
    assert result.exit_code == 0, result.output
    assert host.pushed == []
    assert "declined" in result.output


def test_diff_multiple_targets(monkeypatch, tmp_path):
    hosts = [
        FakeHost(
            "box1", diff=DeployDiff(text="", has_changes=False, summary="no changes")
        ),
        FakeHost(
            "box2", diff=DeployDiff(text="+ set x", has_changes=True, summary="+1")
        ),
        FakeHost(
            "box3", diff=DeployDiff(text="", has_changes=False, summary="no changes")
        ),
    ]
    filename = wire(monkeypatch, tmp_path, hosts)

    result = CliRunner().invoke(
        config, ["diff", "box1", "box3", "--filename", filename]
    )
    assert result.exit_code == 0, result.output
    assert "box1" in result.output
    assert "box3" in result.output
    # box2 was not selected.
    assert "box2" not in result.output


def test_deploy_multiple_targets(monkeypatch, tmp_path):
    hosts = [
        FakeHost(
            "box1", diff=DeployDiff(text="+ set x", has_changes=True, summary="+1")
        ),
        FakeHost(
            "box2", diff=DeployDiff(text="+ set y", has_changes=True, summary="+1")
        ),
    ]
    filename = wire(monkeypatch, tmp_path, hosts)

    result = CliRunner().invoke(
        config, ["deploy", "box1", "box2", "--yes", "--filename", filename]
    )
    assert result.exit_code == 0, result.output
    assert hosts[0].pushed and hosts[1].pushed


def test_all_is_mutually_exclusive_with_named_targets(monkeypatch, tmp_path):
    filename = wire(monkeypatch, tmp_path, [FakeHost("box1")])

    result = CliRunner().invoke(config, ["diff", "all", "box1", "--filename", filename])
    assert result.exit_code != 0
    assert "cannot be combined" in result.output


def test_duplicate_targets_deduplicated(monkeypatch, tmp_path):
    host = FakeHost(
        "box1", diff=DeployDiff(text="", has_changes=False, summary="no changes")
    )
    filename = wire(monkeypatch, tmp_path, [host])

    result = CliRunner().invoke(
        config, ["diff", "box1", "box1", "--filename", filename]
    )
    assert result.exit_code == 0, result.output
    assert result.output.count("box1") == 1


def test_no_targets_is_a_usage_error(monkeypatch, tmp_path):
    filename = wire(monkeypatch, tmp_path, [FakeHost("box1")])
    result = CliRunner().invoke(config, ["diff", "--filename", filename])
    assert result.exit_code != 0
