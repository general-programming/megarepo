packer {
  required_plugins {
    scaleway = {
      version = ">= 1.0.0"
      source  = "github.com/hashicorp/scaleway"
    }
  }
}
