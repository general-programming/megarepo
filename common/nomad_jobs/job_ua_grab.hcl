job "at_ua_grab" {
    type = "system"
    datacenters = ["dc1"]

    group "ua_grab" {
        restart {
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "warrior_ua_grab" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"

            resources {
                memory = 512
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
                image = "atdr.meo.ws/archiveteam/ua-grab"
                force_pull = true
                // got oom killed at 768. 1024 should be enough...
                memory_hard_limit = 1024
            }
        }
    }
}
