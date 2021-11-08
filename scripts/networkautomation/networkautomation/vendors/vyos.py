from networkautomation.vendors import BaseHost


class VyOSHost(BaseHost):
    DEVICETYPE = "vyos"

    @staticmethod
    def can_bfd():
        return True
