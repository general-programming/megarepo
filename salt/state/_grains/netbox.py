import salt.modules.netbox


def netbox_leases():
    grains = {
        "netbox_dns": {
            "addresses": [],
            "ptr_records": [],
        }
    }

    entries = salt.modules.netbox.get_leases()

    for entry in entries:
        if entry.get("address"):
            grains["netbox_dns"]["addresses"].append(entry["address"])
        if entry.get("ptr_record"):
            grains["netbox_dns"]["ptr_records"].append(entry["ptr_record"])

    return grains
    return grains
