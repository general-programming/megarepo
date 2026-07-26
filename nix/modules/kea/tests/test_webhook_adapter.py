import webhook_adapter


def test_leases_collects_indexed_env(monkeypatch) -> None:
    monkeypatch.setenv("LEASES4_SIZE", "2")
    monkeypatch.setenv("LEASES4_AT0_ADDRESS", "10.3.2.10")
    monkeypatch.setenv("LEASES4_AT0_HWADDR", "bc:24:11:6a:62:b3")
    monkeypatch.setenv("LEASES4_AT0_HOSTNAME", "sea1-k8s-0.")
    monkeypatch.setenv("LEASES4_AT1_ADDRESS", "10.3.2.11")
    monkeypatch.setenv("LEASES4_AT1_HWADDR", "")
    monkeypatch.setenv("LEASES4_AT1_HOSTNAME", "")

    found = webhook_adapter.leases("LEASES4")
    assert found == [
        {"ip": "10.3.2.10", "mac": "bc:24:11:6a:62:b3", "hostname": "sea1-k8s-0"},
        {"ip": "10.3.2.11", "mac": "N/A", "hostname": "N/A"},
    ]


def test_leases_bad_or_missing_size(monkeypatch) -> None:
    monkeypatch.delenv("LEASES6_SIZE", raising=False)
    assert webhook_adapter.leases("LEASES6") == []
    monkeypatch.setenv("LEASES6_SIZE", "banana")
    assert webhook_adapter.leases("LEASES6") == []


def test_main_ignores_other_hook_points(monkeypatch, capsys) -> None:
    called = []
    monkeypatch.setattr(webhook_adapter, "webhook_url", lambda: called.append(1))
    monkeypatch.setattr("sys.argv", ["adapter", "lease4_renew"])
    webhook_adapter.main()
    assert called == []
