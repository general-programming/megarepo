terraform {
  required_version = "~> 1.14.0"

  required_providers {
    local = {
      source = "hashicorp/local"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 4.5"
    }
  }

  backend "remote" {
    hostname = "app.terraform.io"
    organization = "genprog"

    workspaces {
      name = "cf-mailhook"
    }
  }
}

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}
