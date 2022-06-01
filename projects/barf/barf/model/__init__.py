import hvac


def get_vault() -> hvac.Client:
    vault = hvac.Client()

    return vault
