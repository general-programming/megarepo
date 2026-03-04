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

module "authentik_app_netbox" {
  source      = "./authentik/app-netbox"
  group_uuids = module.authentik.group_uuids
}

module "authentik_app_sentry" {
  source      = "./authentik/app-sentry"
  group_uuids = module.authentik.group_uuids
}

module "authentik_app_argocd_sea1" {
  source      = "./authentik/app-argocd"
  group_uuids = module.authentik.group_uuids
  domain = "https://sea1-argo.generalprogramming.org"
  cluster = "sea1"
  slug = "argocd_sea1"
}

module "authentik_app_argocd_fmt2" {
  source      = "./authentik/app-argocd"
  group_uuids = module.authentik.group_uuids
  domain = "https://fmt2-argo.generalprogramming.org"
  cluster = "fmt2"
  slug = "argocd_fmt2"
}
