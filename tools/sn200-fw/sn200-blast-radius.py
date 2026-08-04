#!/usr/bin/env python3
"""What data dies if an SN200 latches right now?

The SN200 latch has no proven data-recovery route: the NVMe surface is
exhausted (docs/sn200-vuc-flash-read.md) and the UART/SBL route has never been
run. So the only thing that actually satisfies "do not lose data" is making
sure nothing on these drives is the last copy.

This answers that question from live cluster state instead of by hand. It reads
PersistentVolumes pinned to SN200-backed nodes, groups them by the workload
that owns them, and asks two things per workload:

  1. how many copies exist, and
  2. how many of those copies are on an SN200

Because a workload replicated across two SN200s is NOT protected in the way the
replica count implies -- both copies sit on the same model with the same
firmware defect, and a common trigger (a TRIM, a power event, a BMC MI storm)
is not independent between them.

Read-only. Talks to the Kubernetes API only, never to a drive.
"""

import argparse
import json
import subprocess
import sys
from collections import defaultdict

# A workload's copies are only meaningfully independent if they are NOT all on
# the same defective drive model.
CRITICAL = "CRITICAL"
HIGH = "HIGH"
OK = "OK"


def kubectl(args: list[str]) -> dict:
    out = subprocess.run(
        ["kubectl"] + args + ["-o", "json"], capture_output=True, text=True
    )
    if out.returncode != 0:
        sys.stderr.write(out.stderr)
        raise SystemExit("kubectl failed: %s" % " ".join(args))
    return json.loads(out.stdout)


def pv_node(pv: dict) -> str:
    """The node a local PV is pinned to, or '' if it is not node-local."""
    terms = (
        pv.get("spec", {})
        .get("nodeAffinity", {})
        .get("required", {})
        .get("nodeSelectorTerms", [])
    )
    for t in terms:
        for m in t.get("matchExpressions", []):
            if m.get("key") == "kubernetes.io/hostname":
                return ",".join(m.get("values", []))
    return ""


def workload_of(namespace: str, claim: str) -> str:
    """Group sibling volumes under one owner.

    CNPG names PVCs <cluster>-<n> and StatefulSets name them <vct>-<sts>-<n>, so
    a trailing ordinal is the join key in both cases. Getting this wrong splits
    a replicated cluster into N apparently-single-copy workloads, which would
    invert the verdict -- so when the suffix is not a plain ordinal, keep the
    name whole rather than guessing.
    """
    base = claim.rsplit("-", 1)
    if len(base) == 2 and base[1].isdigit():
        return "%s/%s" % (namespace, base[0])
    return "%s/%s" % (namespace, claim)


def collect(pvs: dict, sn200_nodes: set, backups: int) -> list[dict]:
    copies = defaultdict(list)
    for pv in pvs.get("items", []):
        node = pv_node(pv)
        if not node:
            continue  # ceph/network storage -- not in this blast radius
        cr = pv.get("spec", {}).get("claimRef", {})
        ns, name = cr.get("namespace", ""), cr.get("name", "")
        if not ns:
            continue
        copies[workload_of(ns, name)].append(
            {
                "node": node,
                "size": pv.get("spec", {}).get("capacity", {}).get("storage", "?"),
                "pvc": name,
                "on_sn200": node in sn200_nodes,
            }
        )

    rows = []
    for wl, vols in copies.items():
        on = [v for v in vols if v["on_sn200"]]
        if not on:
            continue  # nothing of this workload is on an SN200
        total, n_sn200 = len(vols), len(on)
        if total == 1:
            verdict, why = CRITICAL, "only one copy exists, and it is on an SN200"
        elif n_sn200 == total:
            verdict, why = (
                HIGH,
                "all %d copies are on SN200s -- same model, same defect, "
                "not independent" % total,
            )
        else:
            verdict, why = (
                OK,
                "%d of %d copies are off-SN200" % (total - n_sn200, total),
            )
        if verdict != OK and backups == 0:
            why += "; NO backup exists, so there is no restore path either"
        rows.append(
            {
                "workload": wl,
                "verdict": verdict,
                "why": why,
                "copies": total,
                "on_sn200": n_sn200,
                "volumes": sorted(on, key=lambda v: v["node"]),
            }
        )
    order = {CRITICAL: 0, HIGH: 1, OK: 2}
    return sorted(rows, key=lambda r: (order[r["verdict"]], r["workload"]))


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--node",
        action="append",
        default=[],
        help="node whose SN200 to model as failing (repeatable). "
        "Default: every node known to have one.",
    )
    ap.add_argument("--fixture", help="read PVs from a JSON file instead of the API")
    ap.add_argument(
        "--backups",
        type=int,
        default=None,
        help="number of backups that exist (default: count them via the API)",
    )
    args = ap.parse_args()

    sn200 = set(args.node) or {"sea1-k8s-0", "sea1-k8s-1", "sea1-k8s-2"}

    if args.fixture:
        with open(args.fixture) as fh:
            pvs = json.load(fh)
        backups = args.backups or 0
    else:
        pvs = kubectl(["get", "pv"])
        if args.backups is None:
            try:
                backups = len(
                    kubectl(["get", "backups.postgresql.cnpg.io", "-A"]).get(
                        "items", []
                    )
                )
            except SystemExit:
                backups = 0
        else:
            backups = args.backups

    rows = collect(pvs, sn200, backups)

    print("=== SN200 blast radius ===")
    print("  modelling failure of the SN200 in: %s" % ", ".join(sorted(sn200)))
    print("  backups that exist cluster-wide: %d" % backups)
    if backups == 0:
        print("  !! zero backups. Replication is the ONLY thing protecting any of")
        print("  !! this, and a latched SN200 has no proven recovery route.")
    print()

    if not rows:
        print("  Nothing node-local is on an SN200. Nothing to lose here.")
        return 0

    for r in rows:
        mark = {CRITICAL: "***", HIGH: " ! ", OK: "   "}[r["verdict"]]
        print("%s %-8s %s" % (mark, r["verdict"], r["workload"]))
        print("      %s" % r["why"])
        for v in r["volumes"]:
            print("      %-14s %-8s %s" % (v["node"], v["size"], v["pvc"]))
        print()

    n_crit = sum(1 for r in rows if r["verdict"] == CRITICAL)
    n_high = sum(1 for r in rows if r["verdict"] == HIGH)
    n_ok = len(rows) - n_crit - n_high
    print("--- %d CRITICAL, %d HIGH, %d OK ---" % (n_crit, n_high, n_ok))
    if n_crit:
        print()
        print("CRITICAL means: one latch, and that data is gone permanently.")
        print("There is no NVMe-surface recovery -- see docs/sn200-runbook.md.")
    return 1 if n_crit else 0


if __name__ == "__main__":
    sys.exit(main())
