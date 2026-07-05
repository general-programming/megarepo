from barf.vendors import BaseHost


class EosHost(BaseHost):
    DEVICETYPE = "eos"
    NAPALM_DRIVER = "eos"

    def _napalm_address(self):
        # eAPI runs over HTTPS, so probe 443 instead of SSH.
        return self.management_ip
