"""Tests for sn200-blast-radius.py.

The tool exists to answer one question honestly: what is the last copy? The
verdicts drive real decisions about production databases, so the tests care
most about the ways it could be reassuringly wrong.
"""

import importlib.util
import json
import os

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOLS = os.path.dirname(HERE)

spec = importlib.util.spec_from_file_location(
    "blast_radius", os.path.join(TOOLS, "sn200-blast-radius.py")
)
br = importlib.util.module_from_spec(spec)
spec.loader.exec_module(br)

SN200 = {"sea1-k8s-0", "sea1-k8s-1"}


def pv(node, ns, claim, size="8Gi"):
    d = {
        "spec": {
            "capacity": {"storage": size},
            "claimRef": {"namespace": ns, "name": claim},
        }
    }
    if node:
        d["spec"]["nodeAffinity"] = {
            "required": {
                "nodeSelectorTerms": [
                    {
                        "matchExpressions": [
                            {"key": "kubernetes.io/hostname", "values": [node]}
                        ]
                    }
                ]
            }
        }
    return d


def run(pvs, backups=0, nodes=SN200):
    return br.collect({"items": pvs}, nodes, backups)


def test_single_copy_on_an_sn200_is_critical():
    rows = run([pv("sea1-k8s-0", "shared-db", "shared-timescaledb-7", "256Gi")])
    assert len(rows) == 1
    assert rows[0]["verdict"] == br.CRITICAL
    assert "only one copy" in rows[0]["why"]
    assert "NO backup" in rows[0]["why"]


def test_two_copies_both_on_sn200s_is_not_treated_as_safe():
    """The trap this tool exists for. A replica count of 2 reads as protected,
    but both copies are on the same defective model with a shared trigger --
    that is not independent redundancy."""
    rows = run(
        [
            pv("sea1-k8s-0", "netbox", "netbox-db-10"),
            pv("sea1-k8s-1", "netbox", "netbox-db-6"),
        ]
    )
    assert rows[0]["verdict"] == br.HIGH
    assert rows[0]["copies"] == 2
    assert "same model, same defect" in rows[0]["why"]


def test_a_copy_off_the_sn200s_clears_it():
    rows = run(
        [
            pv("sea1-k8s-0", "netbox", "netbox-db-10"),
            pv("sea1-k8s-1", "netbox", "netbox-db-6"),
            pv("sea1-k8s-103-0", "netbox", "netbox-db-12"),
        ]
    )
    assert rows[0]["verdict"] == br.OK
    assert "1 of 3 copies are off-SN200" in rows[0]["why"]


def test_backups_existing_removes_the_no_restore_path_clause():
    rows = run([pv("sea1-k8s-0", "shared-db", "shared-timescaledb-7")], backups=3)
    assert rows[0]["verdict"] == br.CRITICAL  # still the last live copy
    assert "NO backup" not in rows[0]["why"]  # but a restore path exists


def test_siblings_are_grouped_so_a_replicated_cluster_is_not_split():
    """If the ordinal suffix were not stripped, netbox-db-10 and netbox-db-6
    would each look like a separate single-copy workload -- turning one HIGH
    into two CRITICALs and destroying the tool's credibility."""
    assert br.workload_of("netbox", "netbox-db-10") == br.workload_of(
        "netbox", "netbox-db-6"
    )
    rows = run(
        [
            pv("sea1-k8s-0", "netbox", "netbox-db-10"),
            pv("sea1-k8s-1", "netbox", "netbox-db-6"),
        ]
    )
    assert len(rows) == 1


def test_a_non_ordinal_suffix_is_not_guessed_at():
    """meilisearch has no ordinal. Stripping a non-numeric suffix would fold
    unrelated volumes together and hide a single copy inside a fake group."""
    assert br.workload_of("shared-db", "meilisearch") == "shared-db/meilisearch"
    rows = run([pv("sea1-k8s-1", "shared-db", "meilisearch", "32Gi")])
    assert rows[0]["verdict"] == br.CRITICAL


def test_network_storage_is_out_of_scope_not_silently_counted():
    """A PV with no node affinity is ceph/RBD -- it is not on an SN200 and must
    not appear, or the report inflates and stops being actionable."""
    rows = run([pv(None, "some-ns", "rbd-claim-0")])
    assert rows == []


def test_workloads_entirely_off_the_sn200s_are_excluded():
    rows = run([pv("sea1-k8s-103-0", "shared-db", "data-scylladb-sea1-sea1-1")])
    assert rows == []


def test_critical_sorts_above_high_above_ok():
    rows = run(
        [
            pv("sea1-k8s-0", "a", "a-db-1"),
            pv("sea1-k8s-1", "a", "a-db-2"),
            pv("sea1-k8s-0", "b", "b-solo"),
            pv("sea1-k8s-0", "c", "c-db-1"),
            pv("sea1-k8s-1", "c", "c-db-2"),
            pv("sea1-k8s-103-0", "c", "c-db-3"),
        ]
    )
    assert [r["verdict"] for r in rows] == [br.CRITICAL, br.HIGH, br.OK]


def test_multiple_volumes_of_one_workload_on_the_same_node_still_count_separately():
    """Two PVCs of one cluster pinned to the SAME node is not redundancy at
    all -- one drive holds both. It must not read as 2 independent copies."""
    rows = run([pv("sea1-k8s-0", "x", "x-db-1"), pv("sea1-k8s-0", "x", "x-db-2")])
    assert rows[0]["verdict"] == br.HIGH
    assert rows[0]["on_sn200"] == 2
    assert {v["node"] for v in rows[0]["volumes"]} == {"sea1-k8s-0"}


@pytest.mark.parametrize("size", ["256Gi", "32Gi"])
def test_volume_size_is_reported_so_the_cost_is_visible(size):
    rows = run([pv("sea1-k8s-0", "shared-db", "solo", size)])
    assert rows[0]["volumes"][0]["size"] == size


def test_summary_counts_do_not_double_count(capsys, tmp_path, monkeypatch):
    """The OK count is a count, not the row total -- printing len(rows) there
    claimed 13 OK alongside 2 CRITICAL and 11 HIGH on the live cluster, i.e. it
    reported every at-risk workload as also being fine."""
    fixture = tmp_path / "pv.json"
    fixture.write_text(
        json.dumps(
            {
                "items": [
                    pv("sea1-k8s-0", "b", "b-solo"),
                    pv("sea1-k8s-0", "a", "a-db-1"),
                    pv("sea1-k8s-1", "a", "a-db-2"),
                ]
            }
        )
    )
    monkeypatch.setattr(
        "sys.argv",
        [
            "x",
            "--fixture",
            str(fixture),
            "--backups",
            "0",
            "--node",
            "sea1-k8s-0",
            "--node",
            "sea1-k8s-1",
        ],
    )
    br.main()
    assert "1 CRITICAL, 1 HIGH, 0 OK" in capsys.readouterr().out
