provider "vault" {
}

provider "authentik" {
}

terraform {
  required_providers {
    authentik = {
      source = "goauthentik/authentik"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

module "authentik" {
  source   = "./authentik"
}
