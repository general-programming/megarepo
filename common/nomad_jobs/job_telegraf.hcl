job "telegraf" {
    type = "system"
    datacenters = ["dc1"]

    constraint {
        operator = "distinct_hosts"
        value = "true"
    }

    group "telegrafs" {
        restart {
            attempts = 10
        }

        task "daemon" {
            driver = "docker"
            user = "root"

            resources {
                memory = 128
            }

            // TODO/XXX Vault TLS + new dynamic infra.
            artifact {
                source = "https://f002.backblazeb2.com/file/REDACTED/telegraf/ca.pem"
            }

            artifact {
                source = "https://f002.backblazeb2.com/file/REDACTED/telegraf/cert.pem"
            }

            artifact {
                source = "https://f002.backblazeb2.com/file/REDACTED/telegraf/key.pem"
            }

            artifact {
                source = "https://f002.backblazeb2.com/file/REDACTED/telegraf/client.conf"
            }

            config {
                image = "telegraf:1.18.1-alpine"
                network_mode = "host"
                volumes = [
                    "/:/hostfs:ro",
                    "local/ca.pem:/etc/telegraf/ca.pem:ro",
                    "local/cert.pem:/etc/telegraf/cert.pem:ro",
                    "local/key.pem:/etc/telegraf/key.pem:ro",
                    "local/client.conf:/etc/telegraf/telegraf.conf:ro"
                ]
            }

            env {
                HOST_ETC = "/hostfs/etc"
                HOST_PROC = "/hostfs/proc"
                HOST_SYS = "/hostfs/sys"
                HOST_VAR = "/hostfs/var"
                HOST_RUN = "/hostfs/run"
                HOST_MOUNT_PREFIX = "/hostfs"
            }
        }
    }
}
