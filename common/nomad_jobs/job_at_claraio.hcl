job "at_claraio" {
    type = "system"
    datacenters = ["dc1"]

    constraint {
        attribute = "${node.class}"
        operator  = "=="
        value = "bigbiglarge"
    }

    group "claraio" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "warrior-claraio" {
            driver = "docker"
            resources {
                memory = 384
            }

            config {
                args = [
                    "--concurrent",
                    "5",
                    "nepeat"
                ]
                logging {
                    type = "loki"
                    config {
                        loki-url = "http://loki.fmt2.generalprogramming.org/loki/api/v1/push"
                        loki-retries = "5"
                        loki-batch-size = "400"
                        loki-external-labels = "container_name={{.Name}},group=archiveteam,job=${NOMAD_JOB_NAME}"
                    }
                }
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
                image = "atdr.meo.ws/archiveteam/claraio-grab"
                force_pull = true
                // worst case scenario, 512 gives enough room for disaster
                memory_hard_limit = 512
            }
        }
    }
}
