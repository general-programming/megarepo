packer {
  required_plugins {
    oracle = {
      version = ">= 1.0.1"
      source  = "github.com/hashicorp/oracle"
    }
  }
}
