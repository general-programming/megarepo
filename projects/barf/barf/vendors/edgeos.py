from barf.vendors import BaseHost


class EdgeOSHost(BaseHost):
    DEVICETYPE = "edgeos"

    def interface_prefix(self, interface):
        if interface.management:
            interface_type = "dummy"
        else:
            interface_type = "ethernet"

        set_string = f"set interfaces {interface_type} {interface.name}"

        if interface.untagged_vlan:
            set_string += f" vif {interface.untagged_vlan.vid}"

        return set_string
