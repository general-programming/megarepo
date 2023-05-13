service {
    name = "scylla-native"
    id = "scylla-native"
    port = 19042

    tags = [
        "scylla",
        "scyllanative"
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
            id = "scylla-native"
            name = "Scylla Native port on 19042"
            interval = "10s"
            tcp = "localhost:19042"
            timeout = "5s"
        }
    ]
}
