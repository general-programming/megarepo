service {
    name = "proxmox"
    id = "proxmox"
    port = 8006

    tags = [
        "proxmox",
        "server"
    ]

    checks = [
        {
            interval = "10s"
            http = "https://localhost:8006"
            tls_skip_verify = true
            timeout = "5s"
        }
    ]
}
