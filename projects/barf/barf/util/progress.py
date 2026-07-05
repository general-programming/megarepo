import time

import click


def format_bytes(count: float) -> str:
    """Format a byte count the way pv does (MiB/GiB)."""
    mib = count / 2**20
    if mib >= 1024:
        return f"{mib / 1024:.2f}GiB"
    return f"{mib:.1f}MiB"


def format_duration(seconds: float) -> str:
    """Format seconds as h:mm:ss."""
    seconds = max(0, int(seconds))
    return f"{seconds // 3600}:{seconds % 3600 // 60:02d}:{seconds % 60:02d}"


class PvProgress:
    """A pv(1)-style single-line transfer progress display.

    Renders like::

        label  175.0MiB / 660.0MiB [=======>      ]  26%  3.5MiB/s  0:00:50  ETA 0:02:20
    """

    BAR_WIDTH = 30
    MIN_INTERVAL = 0.2

    def __init__(self, label: str, total: int, clock=time.monotonic):
        self.label = label
        self.total = total
        self.clock = clock
        self.start = clock()
        self.sent = 0
        self._last_render = float("-inf")

    def update(self, sent: int) -> None:
        self.sent = min(sent, self.total)
        now = self.clock()
        if now - self._last_render < self.MIN_INTERVAL and self.sent < self.total:
            return
        self._last_render = now
        click.echo("\r" + self.render(), nl=False)

    def finish(self) -> None:
        """Render the final state and release the line."""
        click.echo("\r" + self.render())

    def render(self) -> str:
        elapsed = max(self.clock() - self.start, 1e-6)
        rate = self.sent / elapsed

        if self.total:
            percent = int(self.sent * 100 / self.total)
            filled = int(self.BAR_WIDTH * self.sent / self.total)
        else:
            percent, filled = 100, self.BAR_WIDTH

        if 0 < filled < self.BAR_WIDTH:
            bar = "=" * (filled - 1) + ">"
        else:
            bar = "=" * filled

        if rate > 0 and self.sent < self.total:
            eta = f"ETA {format_duration((self.total - self.sent) / rate)}"
        else:
            eta = "ETA 0:00:00"

        return (
            f"{self.label}  {format_bytes(self.sent)} / {format_bytes(self.total)}"
            f" [{bar:<{self.BAR_WIDTH}}] {percent:>3}%"
            f"  {format_bytes(rate)}/s  {format_duration(elapsed)}  {eta}"
        )
