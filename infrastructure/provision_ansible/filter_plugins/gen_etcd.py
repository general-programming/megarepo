def gen_etcd_hosts(hosts, port=2380, with_hostname=False):
    etcd_hosts = []

    for host in list(hosts.values()):
        if "etcd_name" not in host:
            continue

        etcd_name_string = (host["etcd_name"] + "=") if with_hostname else ""
        etcd_hosts.append(etcd_name_string + "http://" + host["ansible_vpn0"]["ipv4"]["address"] + ":" + str(port))

    return ",".join(etcd_hosts)

class FilterModule(object):
    def filters(self):
        return {
            'etcd_hosts': gen_etcd_hosts
        }
