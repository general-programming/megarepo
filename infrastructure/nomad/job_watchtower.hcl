job "watchtower" {
    type = "system"
    datacenters = ["dc1"]

    group "watchtower" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "watchtower" {
            driver = "docker"

            resources {
                memory = 48
            }

            config {
                args = [
                    "--label-enable",
                    "--cleanup",
                    "--interval", "3600"
                ]
                image = "containrrr/watchtower"

                mount {
                    type = "bind"
                    target = "/var/run/docker.sock"
                    source = "/var/run/docker.sock"
                    readonly = true
                    bind_options {
                        propagation = "rprivate"
                    }
                }
            }
        }
    }
}
