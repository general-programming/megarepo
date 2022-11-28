job "at_grab" {
    type = "system"
    datacenters = ["dc1"]

    group "grab" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "warrior-grab" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"

            resources {
                memory = 512
            }

            config {
                args = [
                    "--concurrent",
                    "15",
                    "nepeat"
                ]
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
                logging {
                    type = "loki"
                    config {
                        loki-url = "http://loki.service.fmt2.consul:3100/loki/api/v1/push"
                        loki-retries = "5"
                        loki-batch-size = "400"
                        loki-external-labels = "container_name={{.Name}},group=archiveteam,job=${NOMAD_JOB_NAME}"
                    }
                }
                image = "atdr.meo.ws/archiveteam/grab-grab"
                force_pull = true
                // worst case scenario, 1024 gives enough room for disaster
                memory_hard_limit = 1024
            }
        }
    }
}
