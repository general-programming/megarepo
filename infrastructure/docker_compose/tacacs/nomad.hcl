job "tacacs" {
    datacenters = ["dc1"]

    // run filebeat on server nodes only
    constraint {
        attribute = "${node.class}"
        operator  = "=="
        value = "server"
    }

    group "tacacs" {
        count = 5

        restart {
            attempts = 4
            mode = "delay"
        }

        meta {
            deploy_nonce = 1
        }

        network {
            port "tacacs" {
                static = 49
                to = 49
            }
        }

        service {
            port = "49"
            name = "tacacs"
            address_mode = "host"

            check {
                address_mode = "host"
                type     = "tcp"
                port     = "49"
                interval = "10s"
                timeout  = "5s"
            }
        }

        task "tacacs" {
            driver = "docker"

            resources {
                memory = 256
            }

            vault {
                policies = ["access-cluster-secrets"]
            }

            template {
            data = <<EOH
id = spawnd {
    listen = { port = 49 }
    spawn = {
        instances min = 1
        instances max = 10
    }
    background = no
}

id = tac_plus {
    debug = PARSE CONFIG AUTHOR AUTHEN

    log = stdout {
        destination = /proc/1/fd/1
    }

    log = stderr {
        destination = /dev/stderr
    }

    log = file {
        destination = /var/log/tac_plus.log
    }

    authentication log = stderr
    authorization log = stderr
    accounting log = stderr

    authentication log = file
    authorization log = file
    accounting log = file

    mavis module = external {
        setenv LDAP_SERVER_TYPE = "microsoft"

{{with secret "cluster-secrets/data/server_tacacs_ldap"}}
        #If you are using Microsoft Global Catalog with regular LDAP (non-SSL)
        setenv LDAP_HOSTS = "{{.Data.data.hosts}}"
        setenv LDAP_BASE = "{{.Data.data.base_dn}}"
        setenv LDAP_SCOPE = sub
        setenv LDAP_FILTER = "(uid=%s)"

        ## Username + UPN Authentication [example: user@mydomain.lan]
        setenv LDAP_USER = "{{.Data.data.bind_user}}"
        setenv LDAP_PASSWD = "{{.Data.data.bind_pass}}"
{{end}}
        # Setting UNLIMIT_AD_GROUP_MEMBERSHIP to 0 will cause a NACK response if the AD account is a member of more than one security group
        setenv UNLIMIT_AD_GROUP_MEMBERSHIP = 1

        # Clear default setting of tacplus for AD_GROUP_PREFIX
        setenv AD_GROUP_PREFIX = "net"

        # ???
        # setenv EXPAND_AD_GROUP_MEMBERSHIP = 1

        # Setting REQUIRE_TACACS_GROUP_PREFIX to 1 will cause a NACK response if the AD account is not a member of a security group with the required prefix
        setenv REQUIRE_TACACS_GROUP_PREFIX = 1
        setenv FLAG_USE_MEMBEROF = 1

        exec = /tacacs/lib/mavis/mavis_tacplus_ldap.pl
    }

    login backend = mavis
    user backend = mavis
    pap backend = mavis
    skip missing groups = yes

{{ with secret "cluster-secrets/data/tacacs-keys" }}
    {{ range $k, $v := .Data.data }}
    host = {{ $k }} {
        address = {{ $v.address }}
        key = "{{ $v.key }}"
    }
    {{ end }}
{{ end }}

    group = readonly {
        member = readonly

        # Deny  all services by default
        default service = deny

        enable = deny

        ###
        ### Cisco IOS Authentication
        ###
        service = shell  {
            default command = deny

            # Set privilege level to 15 on IOS/XE
            set priv-lvl = 1

            cmd = show {
                permit .*
            }
            cmd = write {
                permit term
            }
            cmd = dir {
                permit .*
            }
            cmd = admin {
                permit .*
            }
            cmd = terminal {
                permit .*
            }
            cmd = more {
                permit .*
            }
            cmd = exit {
                permit .*
            }
            cmd = quit {
                permit .*
            }
        }
    }

    group = superadmin {
        member = superadmin

        default service = permit
        enable = login
        ###
        ### Cisco IOS Authentication
        ###
        service = shell  {
            # Permit all commands
            default command = permit

            # Permit all command attributes
            default attribute = permit

            # Set privilege level to 15 on IOS/XE
            set priv-lvl = 15
        }
        ###
        ### Juniper JunOS Authentication
        ###
        service = junos-exec {
            set local-user-name = superadmins
        }
    }
}
            EOH
                destination = "local/tacacs.cfg"
                change_mode = "restart"
            }

            config {
                image = "registry.generalprogramming.org/tacacs:latest"
                volumes = [
                    "local/tacacs.cfg:/tac_plus.conf",
                ]
                network_mode = "host"
            }
        }
    }
}
