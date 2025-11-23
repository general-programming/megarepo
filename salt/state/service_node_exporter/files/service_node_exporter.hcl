service {
    name = "node_exporter"
    id = "node_exporter"
    port = 9100

    tags = [
        "node_exporter",
        "prometheus",
    ]

    checks = [
        {
            name = "node_exporter on port 9100"
            interval = "10s"
            http = "http://localhost:9100"
            timeout = "5s"
        }
    ]
}
