variable "cloudflare_api_token" {
    description = "Cloudflare API token"
    type        = string
    sensitive   = true
}

variable "excluded_domains" {
    type = list(string)
    default = ["coolmathgam.es", "generalprogramming.org"]
}
