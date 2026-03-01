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

module "authentik_app_grafana" {
  source      = "./authentik/app-grafana"
  group_uuids = module.authentik.group_uuids
}

module "authentik_app_matrix_owo" {
  source      = "./authentik/app-matrix"
  group_uuids = module.authentik.group_uuids
}
