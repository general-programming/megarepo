job "at_yahoo_answers" {
    type = "system"
    datacenters = ["dc1"]

    group "answers" {
        restart {
            attempts = 4
        }

        meta {
            deploy_nonce = 9
        }

        task "answerswarrior" {
            driver = "docker"
            resources {
                // yahoo answers workers use on average 32-80 from observation.
                memory = 128
                // worst case scenario, 256 gives enough room for disaster
                // when https://github.com/hashicorp/nomad/pull/10247 is stable, i'd rather do this
                // memory_max = 256
            }

            config {
                args = [
                    "--concurrent",
                    "5",
                    "nepeat"
                ]
                image = "atdr.meo.ws/archiveteam/yahooanswers-grab"
                force_pull = true
                // worst case scenario, 256 gives enough room for disaster
                memory_hard_limit = 256
            }
        }
    }
}
