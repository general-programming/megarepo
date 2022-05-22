job "at_coub" {
    type = "system"
    datacenters = ["dc1"]

    group "coub" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "warrior-coub" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"

            resources {
                memory = 256
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
                image = "atdr.meo.ws/archiveteam/coub-grab"
                force_pull = true
                // worst case scenario, 512 gives enough room for disaster
                memory_hard_limit = 512
            }
        }
    }
}
