job "at_imgur" {
    type = "system"
    datacenters = ["dc1"]

    constraint {
        attribute = "${node.class}"
        operator  = "=="
        value = "bigbiglarge"
    }

    group "imgur" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 2
        }

        task "warrior-imgur" {
            driver = "docker"
            resources {
                memory = 384
            }

            config {
                args = [
                    "--concurrent",
                    "1",
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
                image = "atdr.meo.ws/archiveteam/imgur-grab"
                force_pull = true
                // worst case scenario, 512 gives enough room for disaster
                memory_hard_limit = 512
            }
        }
    }
}
