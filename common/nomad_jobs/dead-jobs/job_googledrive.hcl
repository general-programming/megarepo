job "at_gdrive" {
    type = "system"
    datacenters = ["dc1"]

    group "gdrives" {
        restart {
            attempts = 4
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        task "gdrive_warrior1" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"
            resources {
                // yahoo answers workers use on average 32-80 from observation.
                memory = 128
                memory_max = 384
            }

            config {
                args = [
                    "--concurrent",
                    "5",
                    "nepeat"
                ]
                image = "atdr.meo.ws/archiveteam/google-drive-grab"
                force_pull = true
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
            }
        }

        task "gdrive_warrior2" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"
            resources {
                // yahoo answers workers use on average 32-80 from observation.
                memory = 128
                memory_max = 384
            }

            config {
                args = [
                    "--concurrent",
                    "5",
                    "nepeat"
                ]
                image = "atdr.meo.ws/archiveteam/google-drive-grab"
                force_pull = true
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
            }

        }

        task "gdrive_warrior3" {
            driver = "docker"
            kill_signal = "SIGINT"
            kill_timeout = "180s"
            resources {
                // yahoo answers workers use on average 32-80 from observation.
                memory = 128
                memory_max = 384
            }

            config {
                args = [
                    "--concurrent",
                    "5",
                    "nepeat"
                ]
                image = "atdr.meo.ws/archiveteam/google-drive-grab"
                force_pull = true
                labels = {
                    "com.centurylinklabs.watchtower.enable" = "true"
                }
            }
        }
    }
}
