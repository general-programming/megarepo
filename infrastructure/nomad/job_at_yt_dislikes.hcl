job "at_ytdislikes" {
    type = "system"
    datacenters = ["dc1"]

    group "ytdislikes" {
        restart {
            attempts = 4
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "ytdislikes_warrior1" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"
            resources {
                memory = 128
                memory_max = 384
            }

            config {
                args = [
                    "--concurrent",
                    "20",
                    "nepeat"
                ]
                image = "atdr.meo.ws/archiveteam/youtube-dislikes-grab"
                force_pull = true
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
            }
        }
    }
}
