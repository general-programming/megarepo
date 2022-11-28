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
                logging {
                    type = "loki"
                    config {
                        loki-url = "http://loki.service.fmt2.consul:3100/loki/api/v1/push"
                        loki-retries = "5"
                        loki-batch-size = "400"
                        loki-external-labels = "container_name={{.Name}},group=archiveteam,job=${NOMAD_JOB_NAME}"
                    }
                }
                force_pull = true
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
                image = "atdr.meo.ws/archiveteam/terroroftinytown-client-grab"
            }
        }
    }
}
