service {
    name = "scylla-cql"
    id = "scylla-cql"
    port = 9042

    tags = [
        "scylla",
        "cql"
    ]

    checks = [
        {
            id = "scylla-jmx"
            name = "Scylla JMX port on 7199"
            interval = "10s"
            tcp = "localhost:7199"
            timeout = "5s"
        },
        {
            id = "scylla-cql"
            name = "Scylla CQL port on 9042"
            interval = "10s"
            tcp = "localhost:9042"
            timeout = "5s"
        }
    ]
}
