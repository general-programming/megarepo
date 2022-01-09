job "at_yahoo_answers_bulk" {
    datacenters = ["dc1"]

    spread {
        attribute = "${node.unique.id}"
    }

    constraint {
        attribute = "${node.class}"
        operator  = "!="
        value = "server"
    }

    constraint {
        attribute = "${node.class}"
        operator  = "!="
        value = "ec2"
    }

    group "answers" {
        count = 500
        restart {
            attempts = 4
        }

        meta {
            deploy_nonce = 10
        }

        task "warrior" {
            driver = "docker"

            kill_signal = "SIGINT"
            kill_timeout = "180s"
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
                    "2",
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
