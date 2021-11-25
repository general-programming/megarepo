from barf.vendors import BaseHost


class VyOSHost(BaseHost):
    DEVICETYPE = "vyos"

    @staticmethod
    @property
    def can_bfd():
        return True
