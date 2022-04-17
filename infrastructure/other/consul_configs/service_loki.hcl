service {
    name = "loki"
    id   = "loki"
    port = 3100

    checks = [
        {
            id = "loki-http"
            name = "Loki"
            http = "http://localhost:3100/ready"
            method = "GET"
            interval =  "10s"
            timeout =  "1s"
        }
    ]
}

