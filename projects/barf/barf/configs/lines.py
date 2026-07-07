"""Vendor line builders: dumb string formatters, deliberately not an AST.

The rendered strings are the live-proven contract the diff/deploy
machinery parses; these helpers only kill the recurring bug classes --
unquoted RouterOS values silently matching nothing, and optional
suffixes fighting Jinja's trim_blocks newline swallowing.
"""

from typing import List


def ros_value(value: object) -> str:
    """A RouterOS property value, quoted exactly when it needs to be.

    Unquoted values with spaces silently break (the find-where gotcha),
    so anything containing whitespace, quotes, or backslashes -- or the
    empty string -- is double-quoted with RouterOS escaping; everything
    else renders bare, matching the adopted device style.
    """
    text = str(value)
    if text == "" or any(ch in text for ch in ' \t"\\'):
        escaped = text.replace("\\", "\\\\").replace('"', '\\"')
        return f'"{escaped}"'
    return text


def ros_line(path: str, verb: str, props: dict) -> str:
    """One RouterOS command from a props dict (the pipeline's shape:
    parse output, diff items, and REST payloads are all props dicts).

    Dict order is emission order; None-valued props vanish, so
    optional arguments drop out of the line; values quote themselves
    via ros_value.
    """
    pairs = [
        f"{key}={ros_value(value)}" for key, value in props.items() if value is not None
    ]
    return " ".join([path, verb, *pairs])


def barf_file(path: str, content: List[str]) -> List[str]:
    """The BARF_FILE heredoc stanza writing ``content`` lines to ``path``.

    The linux deploy path parses these blocks back into a path ->
    content map; the quoted delimiter means nothing expands when the
    script is run by hand.
    """
    return [f"cat << 'BARF_FILE' > {path}", *content, "BARF_FILE"]


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
