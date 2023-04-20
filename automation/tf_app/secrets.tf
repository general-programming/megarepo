data "vault_kv_secret_v2" "proxmox_creds" {
  mount = "secret"
  name  = "proxmox_creds_fmt2"
}
