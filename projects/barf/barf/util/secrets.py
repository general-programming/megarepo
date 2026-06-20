from functools import lru_cache

from barf.actions import get_secret


class VaultSecrets:
    """Secret fetcher that uses Vault and caches secrets.

    This lets us avoid hitting Vault for every single secret fetched.
    Static secrets in theory will not change during the lifetime of the script.
    """

    @lru_cache(maxsize=256)
    def __getattr__(self, key: str) -> str:
        """Fetch a secret from Vault.

        Args:
            key (str): The key to fetch.

        Returns:
            str: The secret's value.
        """
        key = key.replace("_", "-").strip()

        return get_secret(key)
