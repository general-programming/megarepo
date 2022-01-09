job "at_urls" {
    datacenters = ["dc1"]

    constraint {
        attribute = "${node.class}"
        operator  = "!="
        value = "ec2"
    }

    spread {
        attribute = "${node.unique.id}"
    }

    group "urls" {
        count = 24

        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "urlwarrior" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"

            resources {
                memory = 256
                memory_max = 768
            }

            config {
                args = [
                    "--concurrent",
                    "20",
                    "nepeat"
                ]
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
                image = "atdr.meo.ws/archiveteam/urls-grab"
                force_pull = true
            }
        }
    }
}
