job "at_urlteam" {
    type = "system"
    datacenters = ["dc1"]

    constraint {
        operator = "distinct_hosts"
        value = "true"
    }

    group "urlteams" {
        constraint {
            operator = "distinct_hosts"
            value = "true"
        }

        restart {
            mode = "delay"
        }

        task "urlteam" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"

            resources {
                memory = 64
                memory_max = 128
            }

            config {
                args = [
                    "--concurrent",
                    "2",
                    "nepeat"
                ]
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
                image = "atdr.meo.ws/archiveteam/terroroftinytown-client-grab"
            }
        }
    }
}
