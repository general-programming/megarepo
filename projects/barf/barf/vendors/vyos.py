from barf.vendors import BaseHost


class VyOSHost(BaseHost):
    DEVICETYPE = "vyos"

    @staticmethod
    @property
    def can_bfd():
        return True

    @property
    def device_username(self):
        return "vyos"

    def interface_prefix(self, interface):
        if interface.management:
            interface_type = "dummy"
        else:
            interface_type = "ethernet"

        set_string = f"set interfaces {interface_type} {interface.name}"

        if interface.untagged_vlan:
            set_string += f" vif {interface.untagged_vlan.vid}"

        return set_string
