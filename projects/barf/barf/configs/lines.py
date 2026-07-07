"""Vendor line builders: dumb string formatters, deliberately not an AST.

The rendered strings are the live-proven contract the diff/deploy
machinery parses; these helpers only kill the recurring bug classes --
unquoted RouterOS values silently matching nothing, and optional
suffixes fighting Jinja's trim_blocks newline swallowing.
"""

from typing import Optional


def ros_kv(key: str, value: object, quote: bool = False) -> str:
    """A RouterOS ``key=value`` token; empty when value is None.

    None (not just falsy) drops the token so optional arguments vanish
    from the line, while real values like ``0`` still render. ``quote``
    double-quotes the value -- required for anything that can contain
    spaces (comments, keys) and for find-where values.
    """
    if value is None:
        return ""
    if quote:
        return f'{key}="{value}"'
    return f"{key}={value}"


def ros_line(path: str, verb: str = "add", *pairs: Optional[str]) -> str:
    """One RouterOS command from preformatted ``key=value`` tokens.

    Empty/None tokens (e.g. a skipped ros_kv) vanish, so optional
    arguments never leave double spaces behind.
    """
    tokens = [path, verb, *[pair for pair in pairs if pair]]
    return " ".join(tokens)


def squote(value: object) -> str:
    """A single-quoted VyOS value."""
    return f"'{value}'"


def vyos_set(*tokens: object) -> str:
    """A VyOS ``set`` command from path tokens; None tokens vanish.

    Quoting is per-node and the caller's choice (the fleet's committed
    quoting is inconsistent and path diffs are textual): wrap values in
    squote() exactly where the template quoted them.
    """
    return "set " + " ".join(str(token) for token in tokens if token is not None)
