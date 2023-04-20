terraform {
  required_providers {
    proxmox = {
      source = "Telmate/proxmox"
      version = "2.9.14"
    }
  }
}

provider "vault" {
}

provider "proxmox" {
    pm_api_url = "https://10.65.67.100:8006/api2/json"

    pm_user = vault_kv_secret_v2.proxmox_creds.user
    pm_api_token_id = vault_kv_secret_v2.proxmox_creds.token_id
    pm_api_token_secret = vault_kv_secret_v2.proxmox_creds.token_secret
}
