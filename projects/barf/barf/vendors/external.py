from barf.vendors import BaseHost


class ExternalHost(BaseHost):
    DEVICETYPE = "external"
    TEMPLATABLE = False
