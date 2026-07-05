from barf.util.progress import PvProgress, format_bytes, format_duration


def test_format_bytes_mib():
    assert format_bytes(10 * 2**20) == "10.0MiB"
    assert format_bytes(0) == "0.0MiB"


def test_format_bytes_gib():
    assert format_bytes(2 * 2**30) == "2.00GiB"


def test_format_duration():
    assert format_duration(0) == "0:00:00"
    assert format_duration(50) == "0:00:50"
    assert format_duration(3725) == "1:02:05"


def make_progress(total=200 * 2**20):
    clock = {"now": 0.0}
    progress = PvProgress("copy x.iso", total, clock=lambda: clock["now"])
    return progress, clock


def test_render_looks_like_pv():
    progress, clock = make_progress()
    clock["now"] = 10.0
    progress.sent = 10 * 2**20

    line = progress.render()
    assert "copy x.iso" in line
    assert "10.0MiB / 200.0MiB" in line
    assert "  5%" in line
    assert "1.0MiB/s" in line
    assert "0:00:10" in line
    # 190 MiB left at 1 MiB/s.
    assert "ETA 0:03:10" in line


def test_render_complete_has_full_bar_and_zero_eta():
    progress, clock = make_progress()
    clock["now"] = 100.0
    progress.sent = progress.total

    line = progress.render()
    assert "=" * PvProgress.BAR_WIDTH in line
    assert "100%" in line
    assert "ETA 0:00:00" in line


def test_render_partial_bar_has_arrow_head():
    progress, clock = make_progress()
    clock["now"] = 10.0
    progress.sent = progress.total // 2

    line = progress.render()
    assert "=>" in line


def test_update_is_throttled(capsys):
    progress, clock = make_progress()

    progress.update(2**20)
    clock["now"] = 0.05  # within MIN_INTERVAL
    progress.update(2 * 2**20)
    assert capsys.readouterr().out.count("\r") == 1

    clock["now"] = 1.0  # past MIN_INTERVAL
    progress.update(3 * 2**20)
    assert capsys.readouterr().out.count("\r") == 1


def test_finish_releases_the_line(capsys):
    progress, clock = make_progress()
    progress.finish()

    out = capsys.readouterr().out
    assert out.startswith("\r")
    assert out.endswith("\n")
