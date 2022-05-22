job "at_ukr_net" {
    type = "system"
    datacenters = ["dc1"]

    group "ukr-net" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "warrior-ukr-net" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"

            resources {
                memory = 384
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
                image = "atdr.meo.ws/archiveteam/ukr-net-grab"
                force_pull = true
                // worst case scenario, 512 gives enough room for disaster
                memory_hard_limit = 512
            }
        }
    }
}
