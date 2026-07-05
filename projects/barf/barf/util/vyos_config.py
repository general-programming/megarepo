"""Local VyOS config parsing and diffing.

Both the rendered templates (flat ``set ...`` command lists) and the
HTTPS API's ``/retrieve`` JSON tree are normalized into one canonical
representation: a set of path tuples, one per ``set`` command. Diffing
is then plain set arithmetic, done entirely on this machine — no config
session is ever opened on the device.

Merge semantics: barf deploys are merge candidates, so a diff reports
what a merge would add or replace. Config that exists only on the
device is reported separately for information; a merge never deletes
it.

Ownership is exclusion-based: the rendered config is the full truth
everywhere by default, and device paths the candidate does not render
become real deletions — except under the "kept" path prefixes a vendor
declares (see ``VyOSHost.KEPT_PATHS``), which cover device-managed
config and sections not yet modeled in network.yml.
"""

import shlex
from dataclasses import dataclass, field
from typing import List, Optional, Set, Tuple, Union

ConfigPaths = Set[Tuple[str, ...]]
PathPrefixes = Tuple[Tuple[str, ...], ...]

# Path components whose immediate child value is a secret. The value is
# still diffed, only the display is redacted.
_SECRET_NODES = {
    "private-key",
    "secret",
    "password",
    "passphrase",
    "pre-shared-secret",
    "plaintext-password",
    "encrypted-password",
}

_REDACTED = "<redacted>"


def parse_set_commands(text: str) -> ConfigPaths:
    """Parse rendered config text into a set of path tuples.

    Only ``set ...`` lines are considered; blanks, comments, and
    ``delete``/op-mode lines are ignored. Values are shlex-tokenized so
    the templates' inconsistent quoting (``'aes256'`` vs ``aes256``)
    normalizes to the same path.
    """
    paths: ConfigPaths = set()
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if not line.startswith("set "):
            continue
        try:
            tokens = shlex.split(line)
        except ValueError:
            # Unbalanced quotes; keep the raw split rather than dying.
            tokens = line.split()
        paths.add(tuple(tokens[1:]))
    return paths


def paths_from_api_json(data: Union[dict, str, list]) -> ConfigPaths:
    """Flatten the ``/retrieve`` ``showConfig`` JSON tree into path tuples.

    Leaves come back as a string (single value), a list of strings
    (multi-value node), or an empty dict (valueless node); each becomes
    the same path tuple its ``set`` command would produce.
    """
    paths: ConfigPaths = set()

    def walk(node, prefix: Tuple[str, ...]) -> None:
        if isinstance(node, dict):
            if not node:
                if prefix:
                    paths.add(prefix)
                return
            for key, child in node.items():
                walk(child, prefix + (str(key),))
        elif isinstance(node, list):
            for value in node:
                paths.add(prefix + (str(value),))
        else:
            paths.add(prefix + (str(node),))

    walk(data, ())
    return paths


def verify_crypt_hash(password: str, hashed: str) -> Optional[bool]:
    """Whether ``password`` matches a unix crypt hash.

    Returns:
        True/False for a ``$6$`` (sha512-crypt) hash, or None when the
        hash uses a scheme we cannot verify (callers should treat None
        as "unknown", not as a mismatch).
    """
    # Imported lazily; passlib is only needed on the diff path. Its
    # hash registry is populated at runtime, which ty cannot see.
    from passlib.hash import sha512_crypt  # ty: ignore[unresolved-import]

    if not hashed.startswith("$6$"):
        return None
    try:
        return sha512_crypt.verify(password, hashed)
    except ValueError:
        return None


def reconcile_hashed_passwords(
    running: ConfigPaths, candidate: ConfigPaths
) -> Tuple[ConfigPaths, ConfigPaths]:
    """Match candidate ``plaintext-password`` against device hashes.

    VyOS hashes ``plaintext-password`` into ``encrypted-password`` on
    commit, so the two sides of a diff never match textually. For each
    candidate plaintext with an encrypted counterpart on the device:

    - hash matches the plaintext: the password is unchanged; the
      candidate path is rewritten to the device's, so no diff shows.
    - hash does not match: the device path is rewritten to the
      candidate's node name, so the diff pairs them as a replacement.
    - hash scheme unknown: both sides are left alone (the plaintext
      shows as an addition rather than being silently swallowed).

    Returns:
        The adjusted (running, candidate) path sets.
    """
    running = set(running)
    candidate = set(candidate)

    for path in list(candidate):
        if len(path) < 2 or path[-2] != "plaintext-password":
            continue
        prefix = path[:-2]

        for device_path in [
            r for r in running if r[:-1] == prefix + ("encrypted-password",)
        ]:
            matches = verify_crypt_hash(path[-1], device_path[-1])
            if matches is True:
                candidate.discard(path)
                candidate.add(device_path)
            elif matches is False:
                running.discard(device_path)
                running.add(prefix + ("plaintext-password", device_path[-1]))

    return running, candidate


@dataclass
class ConfigDiff:
    """The result of diffing a running config against a candidate.

    Attributes:
        added: Paths the deploy would create.
        replaced: Kept device paths sharing a parent with an added path
            — the values a merge on a single-value node would
            overwrite.
        removed: Device paths the deploy deletes (stale config outside
            the kept prefixes).
        device_only: Kept paths on the device that the candidate does
            not mention at all; a deploy leaves them untouched.
    """

    added: List[Tuple[str, ...]] = field(default_factory=list)
    replaced: List[Tuple[str, ...]] = field(default_factory=list)
    removed: List[Tuple[str, ...]] = field(default_factory=list)
    device_only: List[Tuple[str, ...]] = field(default_factory=list)

    @property
    def has_changes(self) -> bool:
        return bool(self.added or self.removed)


def _is_kept(path: Tuple[str, ...], kept: PathPrefixes) -> bool:
    return any(path[: len(prefix)] == prefix for prefix in kept)


def diff_paths(
    running: ConfigPaths, candidate: ConfigPaths, kept: PathPrefixes = ()
) -> ConfigDiff:
    """Diff two path sets; the candidate owns everything not ``kept``.

    Device paths the candidate does not render are real deletions
    (``removed``) by default. Under a ``kept`` prefix they are merely
    informational: ``replaced`` when the candidate adds a different
    value under the same parent path (best-effort — for a multi-value
    node the value would actually survive the merge), ``device_only``
    otherwise.

    Args:
        kept: Config path prefixes excluded from ownership
            (device-managed config, or sections network.yml does not
            model yet).
    """
    added = candidate - running
    stale = running - candidate

    added_parents = {path[:-1] for path in added if len(path) > 1}

    replaced = []
    removed = []
    device_only = []
    for path in sorted(stale):
        if not _is_kept(path, kept):
            removed.append(path)
        elif len(path) > 1 and path[:-1] in added_parents:
            replaced.append(path)
        else:
            device_only.append(path)

    return ConfigDiff(
        added=sorted(added),
        replaced=replaced,
        removed=removed,
        device_only=device_only,
    )


def minimal_delete_paths(
    diff_removed: List[Tuple[str, ...]],
    running: ConfigPaths,
) -> List[Tuple[str, ...]]:
    """Collapse removed leaf paths into the fewest delete commands.

    A prefix is deleted wholly when every running path beneath it is
    being removed (deleting the node also clears empty parents that
    per-leaf deletes would leave behind). Kept paths are never in the
    removed set, so a subtree containing kept config can never fully
    collapse — a delete structurally cannot touch kept config.
    """
    removed_set = set(diff_removed)
    deletes = set()
    for path in sorted(removed_set):
        chosen = path
        for i in range(1, len(path)):
            prefix = path[:i]
            subtree = {r for r in running if r[: len(prefix)] == prefix}
            if subtree and subtree <= removed_set:
                chosen = prefix
                break
        deletes.add(chosen)
    return sorted(deletes)


def redact_path(path) -> Tuple[str, ...]:
    """A copy of ``path`` with its value hidden when under a secret node."""
    path = tuple(path)
    if len(path) > 1 and path[-2] in _SECRET_NODES:
        return path[:-1] + (_REDACTED,)
    return path


def _format_path(path: Tuple[str, ...], redact: bool) -> str:
    if redact:
        path = redact_path(path)
    return " ".join(shlex.quote(component) for component in path)


def format_diff(
    diff: ConfigDiff, redact: bool = True, show_device_only: bool = False
) -> str:
    """Render a ConfigDiff as +/- ``set`` lines.

    Args:
        redact: Replace secret values (private keys, PSKs, passwords)
            with a placeholder in the output.
        show_device_only: Also list paths that exist only on the device.
            They are informational; a merge deploy never removes them.
    """
    lines = []

    for path in diff.removed:
        lines.append(f"- set {_format_path(path, redact)}")
    for path in diff.replaced:
        lines.append(f"- set {_format_path(path, redact)}")
    for path in diff.added:
        lines.append(f"+ set {_format_path(path, redact)}")

    if show_device_only and diff.device_only:
        lines.append("")
        lines.append("# on device but not in the generated config (kept):")
        for path in diff.device_only:
            lines.append(f"  set {_format_path(path, redact)}")
    elif diff.device_only:
        lines.append(
            f"# {len(diff.device_only)} device-only paths hidden (--show-device-only)"
        )

    return "\n".join(lines)


def summarize_diff(diff: ConfigDiff) -> str:
    """A one-line human summary, e.g. ``+12 ~3 -2 (47 device-only)``."""
    if not diff.has_changes:
        return "no changes"
    parts = [f"+{len(diff.added)}"]
    if diff.replaced:
        parts.append(f"~{len(diff.replaced)}")
    if diff.removed:
        parts.append(f"-{len(diff.removed)}")
    if diff.device_only:
        parts.append(f"({len(diff.device_only)} device-only)")
    return " ".join(parts)
