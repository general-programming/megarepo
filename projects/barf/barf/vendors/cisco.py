from barf.vendors import BaseHost


class CiscoHost(BaseHost):
    DEVICETYPE = "cisco"
    NAPALM_DRIVER = "ios"
