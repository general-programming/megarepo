job "at_enjin" {
    type = "system"
    datacenters = ["dc1"]

    constraint {
        attribute = "${node.class}"
        operator  = "=="
        value = "bigbiglarge"
    }

    group "enjin" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 2
        }

        task "warrior-enjin" {
            driver = "docker"
            resources {
                memory = 384
            }

            config {
                args = [
                    "--concurrent",
                    "3",
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
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
                image = "atdr.meo.ws/archiveteam/enjin-grab"
                force_pull = true
                // worst case scenario, 512 gives enough room for disaster
                memory_hard_limit = 512
            }
        }
    }
}
