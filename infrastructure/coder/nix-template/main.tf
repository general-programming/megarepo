terraform {
  required_providers {
    coder = {
      source = "coder/coder"
    }
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}

provider "coder" {
}

variable "use_kubeconfig" {
  type        = bool
  description = <<-EOF
  Use host kubeconfig? (true/false)

  Set this to false if the Coder host is itself running as a Pod on the same
  Kubernetes cluster as you are deploying workspaces to.

  Set this to true if the Coder host is running outside the Kubernetes cluster
  for workspaces. A valid "~/.kube/config" must be present on the Coder host.
  EOF
  default     = false
}

variable "namespace" {
  type        = string
  description = "The Kubernetes namespace to create workspaces in (must exist prior to creating workspaces)."
  default     = "coder-workspaces"
}

variable "workspace_image" {
  type        = string
  description = "The container image used for the workspace. Must have Nix pre-installed at /nix under uid 1000 (see image/Dockerfile), which is used to seed the persisted /nix on first boot."
  default     = "ghcr.io/general-programming/megarepo/coder-nix-workspace:latest"
}

data "coder_parameter" "cpu" {
  name         = "cpu"
  display_name = "CPU"
  description  = "The number of CPU cores"
  default      = "2"
  icon         = "/icon/memory.svg"
  mutable      = true
  option {
    name  = "2 Cores"
    value = "2"
  }
  option {
    name  = "4 Cores"
    value = "4"
  }
  option {
    name  = "6 Cores"
    value = "6"
  }
  option {
    name  = "8 Cores"
    value = "8"
  }
}

data "coder_parameter" "memory" {
  name         = "memory"
  display_name = "Memory"
  description  = "The amount of memory in GB"
  default      = "8"
  icon         = "/icon/memory.svg"
  mutable      = true
  option {
    name  = "8 GB"
    value = "8"
  }
  option {
    name  = "16 GB"
    value = "16"
  }
  option {
    name  = "24 GB"
    value = "24"
  }
  option {
    name  = "32 GB"
    value = "32"
  }
}

data "coder_parameter" "home_disk_size" {
  name         = "home_disk_size"
  display_name = "Home disk size"
  description  = "The size of the home disk in GB. Shared by /home/coder and the persisted Nix store at /nix, so size generously."
  default      = "20"
  type         = "number"
  icon         = "/emojis/1f4be.png"
  mutable      = false
  validation {
    min = 10
    max = 500
  }
}

data "coder_parameter" "dotfiles_uri" {
  name         = "dotfiles_uri"
  display_name = "Dotfiles repository"
  description  = "Optional dotfiles repo URL to apply on first login via `coder dotfiles` (e.g. git@github.com:you/dotfiles.git). Leave blank to skip."
  default      = ""
  type         = "string"
  icon         = "/icon/dotfiles.svg"
  mutable      = true
}

data "coder_parameter" "repo_url" {
  name         = "repo_url"
  display_name = "Config repository"
  description  = "Git repo cloned into the home directory on first boot (e.g. https://github.com/nepeat/nepeat). Leave blank to skip cloning (and Home Manager activation)."
  default      = ""
  type         = "string"
  icon         = "/icon/git.svg"
  mutable      = true
}

data "coder_parameter" "flake_dir" {
  name         = "flake_dir"
  display_name = "Flake directory"
  description  = "Repo-relative directory containing flake.nix (e.g. `nix` for a flake at nix/flake.nix). Leave blank if flake.nix is at the repo root."
  default      = ""
  type         = "string"
  icon         = "/icon/nix.svg"
  mutable      = true
  validation {
    regex = "^[^/].*[^/]$|^[^/]?$"
    error = "Must be a repo-relative path without leading or trailing slashes."
  }
}

data "coder_parameter" "hm_config" {
  name         = "hm_config"
  display_name = "Home Manager configuration"
  description  = "Name of the homeConfigurations flake output to activate on first boot. The config must declare home.username = \"coder\" and home.homeDirectory = \"/home/coder\". Leave blank to skip Home Manager."
  default      = ""
  type         = "string"
  icon         = "/icon/nix.svg"
  mutable      = true
}

provider "kubernetes" {
  # Authenticate via ~/.kube/config or a Coder-specific ServiceAccount, depending on admin preferences
  config_path = var.use_kubeconfig == true ? "~/.kube/config" : null
}

data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

resource "coder_agent" "main" {
  os   = "linux"
  arch = "amd64"
  env = {
    DOTFILES_URI = data.coder_parameter.dotfiles_uri.value
    REPO_URL     = data.coder_parameter.repo_url.value
    FLAKE_DIR    = data.coder_parameter.flake_dir.value
    HM_CONFIG    = data.coder_parameter.hm_config.value
  }
  startup_script = <<-EOT
    set -e

    # Agent startup scripts run in a non-login shell, so profile.d isn't
    # sourced automatically — pull nix onto PATH by hand. The if-form
    # matters under set -e: a bare `[ -f ] && .` propagates the sourced
    # file's (or the failed test's) nonzero status and kills the script.
    if [ -f /etc/profile.d/nix.sh ]; then
      . /etc/profile.d/nix.sh
    fi

    # Optional, one-time dotfiles bootstrap.
    if [ -n "$DOTFILES_URI" ] && [ ! -f "$HOME/.dotfiles-applied" ]; then
      coder dotfiles -y "$DOTFILES_URI" && touch "$HOME/.dotfiles-applied"
    fi

    # Clone the config repo to a fixed, fully-managed path and activate the
    # Home Manager configuration from it. The checkout is disposable state
    # owned by this script — git is the source of truth — so existing clones
    # are force-reset to the remote on every boot rather than merged; only a
    # fetch failure (best-effort) leaves a stale copy behind. HM activation
    # runs on first boot only, gated on a marker in $HOME; -b hm-bak backs
    # up conflicting dotfiles instead of aborting.
    if [ -n "$REPO_URL" ]; then
      REPO_DIR="$HOME/.home_repo"
      if [ -d "$REPO_DIR/.git" ]; then
        git -C "$REPO_DIR" fetch origin && git -C "$REPO_DIR" reset --hard '@{upstream}' || true
      else
        git clone "$REPO_URL" "$REPO_DIR"
      fi
      if [ -n "$HM_CONFIG" ] && [ ! -f "$HOME/.hm-activated" ]; then
        nix run home-manager -- switch -b hm-bak --flake "$REPO_DIR$${FLAKE_DIR:+/$FLAKE_DIR}#$HM_CONFIG"
        touch "$HOME/.hm-activated"
      fi
    fi
  EOT

  # The following metadata blocks are optional. They are used to display
  # information about your workspace in the dashboard. You can remove them
  # if you don't want to display any information.
  metadata {
    display_name = "CPU Usage"
    key          = "0_cpu_usage"
    script       = "coder stat cpu"
    interval     = 10
    timeout      = 1
  }

  metadata {
    display_name = "RAM Usage"
    key          = "1_ram_usage"
    script       = "coder stat mem"
    interval     = 10
    timeout      = 1
  }

  metadata {
    display_name = "Home Disk"
    key          = "3_home_disk"
    script       = "coder stat disk --path $${HOME}"
    interval     = 60
    timeout      = 1
  }

  metadata {
    display_name = "CPU Usage (Host)"
    key          = "4_cpu_usage_host"
    script       = "coder stat cpu --host"
    interval     = 10
    timeout      = 1
  }

  metadata {
    display_name = "Memory Usage (Host)"
    key          = "5_mem_usage_host"
    script       = "coder stat mem --host"
    interval     = 10
    timeout      = 1
  }

  metadata {
    display_name = "Load Average (Host)"
    key          = "6_load_host"
    # get load avg scaled by number of cores
    script   = <<-EOT
      echo "`cat /proc/loadavg | awk '{ print $1 }'` `nproc`" | awk '{ printf "%0.2f", $1/$2 }'
    EOT
    interval = 60
    timeout  = 1
  }
}

resource "kubernetes_persistent_volume_claim_v1" "home" {
  metadata {
    name      = "coder-${data.coder_workspace.me.id}-home"
    namespace = var.namespace
    labels = {
      "app.kubernetes.io/name"     = "coder-pvc"
      "app.kubernetes.io/instance" = "coder-pvc-${data.coder_workspace.me.id}"
      "app.kubernetes.io/part-of"  = "coder"
      "com.coder.resource"         = "true"
      "com.coder.workspace.id"     = data.coder_workspace.me.id
      "com.coder.workspace.name"   = data.coder_workspace.me.name
      "com.coder.user.id"          = data.coder_workspace_owner.me.id
      "com.coder.user.username"    = data.coder_workspace_owner.me.name
    }
    annotations = {
      "com.coder.user.email" = data.coder_workspace_owner.me.email
    }
  }
  wait_until_bound = false
  spec {
    access_modes       = ["ReadWriteOnce"]
    storage_class_name = "local-path"
    resources {
      requests = {
        storage = "${data.coder_parameter.home_disk_size.value}Gi"
      }
    }
  }
}

resource "kubernetes_deployment_v1" "main" {
  count = data.coder_workspace.me.start_count
  depends_on = [
    kubernetes_persistent_volume_claim_v1.home
  ]
  wait_for_rollout = false
  metadata {
    name      = "coder-${data.coder_workspace.me.id}"
    namespace = var.namespace
    labels = {
      "app.kubernetes.io/name"     = "coder-workspace"
      "app.kubernetes.io/instance" = "coder-workspace-${data.coder_workspace.me.id}"
      "app.kubernetes.io/part-of"  = "coder"
      "com.coder.resource"         = "true"
      "com.coder.workspace.id"     = data.coder_workspace.me.id
      "com.coder.workspace.name"   = data.coder_workspace.me.name
      "com.coder.user.id"          = data.coder_workspace_owner.me.id
      "com.coder.user.username"    = data.coder_workspace_owner.me.name
    }
    annotations = {
      "com.coder.user.email" = data.coder_workspace_owner.me.email
    }
  }

  spec {
    replicas = 1
    selector {
      match_labels = {
        "app.kubernetes.io/name"     = "coder-workspace"
        "app.kubernetes.io/instance" = "coder-workspace-${data.coder_workspace.me.id}"
        "app.kubernetes.io/part-of"  = "coder"
        "com.coder.resource"         = "true"
        "com.coder.workspace.id"     = data.coder_workspace.me.id
        "com.coder.workspace.name"   = data.coder_workspace.me.name
        "com.coder.user.id"          = data.coder_workspace_owner.me.id
        "com.coder.user.username"    = data.coder_workspace_owner.me.name
      }
    }
    strategy {
      type = "Recreate"
    }

    template {
      metadata {
        labels = {
          "app.kubernetes.io/name"     = "coder-workspace"
          "app.kubernetes.io/instance" = "coder-workspace-${data.coder_workspace.me.id}"
          "app.kubernetes.io/part-of"  = "coder"
          "com.coder.resource"         = "true"
          "com.coder.workspace.id"     = data.coder_workspace.me.id
          "com.coder.workspace.name"   = data.coder_workspace.me.name
          "com.coder.user.id"          = data.coder_workspace_owner.me.id
          "com.coder.user.username"    = data.coder_workspace_owner.me.name
        }
      }
      spec {
        security_context {
          run_as_user            = 1000
          run_as_non_root        = true
          fs_group               = 1000
          fs_group_change_policy = "OnRootMismatch"
        }

        # Seeds the persisted /nix (mounted below at "/mnt/nix-seed" via the
        # "_nix" subPath of the home PVC) from this same image's own
        # baked-in /nix, but only on first boot. Gated on a marker file
        # written only after the copy finishes. Two hard-won subtleties:
        #  - kubelet creates the subPath dir root-owned, so `cp -a /nix/.`
        #    fatally fails preserving times on the destination root as uid
        #    1000 — copy the top-level entries instead, never "." itself.
        #  - a partial seed leaves read-only (555) store dirs that block
        #    any re-copy, so a missing marker wipes the partial copy first
        #    (chmod u+w before rm; store contents are read-only).
        init_container {
          name              = "seed-nix"
          image             = var.workspace_image
          image_pull_policy = "Always"
          command = ["sh", "-c", <<-EOT
            set -e
            MARKER=/mnt/nix-seed/.seed-complete
            if [ ! -f "$MARKER" ]; then
              if [ -e /mnt/nix-seed/store ] || [ -e /mnt/nix-seed/var ]; then
                echo "Removing partial seed from a previous failed attempt..."
                find /mnt/nix-seed -mindepth 1 -maxdepth 1 ! -name 'lost+found' -exec chmod -R u+w {} +
                find /mnt/nix-seed -mindepth 1 -maxdepth 1 ! -name 'lost+found' -exec rm -rf {} +
              fi
              echo "Seeding persistent /nix from image..."
              cp -a /nix/store /nix/var /mnt/nix-seed/
              touch "$MARKER"
            else
              echo "/nix already seeded, skipping."
            fi
          EOT
          ]
          security_context {
            run_as_user = 1000
          }
          volume_mount {
            name       = "home"
            mount_path = "/mnt/nix-seed"
            sub_path   = "_nix"
          }
        }

        container {
          name              = "dev"
          image             = var.workspace_image
          image_pull_policy = "Always"
          command           = ["sh", "-c", coder_agent.main.init_script]
          security_context {
            run_as_user = 1000
          }
          env {
            name  = "CODER_AGENT_TOKEN"
            value = coder_agent.main.token
          }
          resources {
            requests = {
              "cpu"    = "250m"
              "memory" = "512Mi"
            }
            limits = {
              "cpu"    = "${data.coder_parameter.cpu.value}"
              "memory" = "${data.coder_parameter.memory.value}Gi"
            }
          }
          volume_mount {
            mount_path = "/home/coder"
            name       = "home"
            read_only  = false
          }
          volume_mount {
            mount_path = "/nix"
            name       = "home"
            sub_path   = "_nix"
            read_only  = false
          }
        }

        volume {
          name = "home"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim_v1.home.metadata.0.name
            read_only  = false
          }
        }

        affinity {
          # This affinity attempts to spread out all workspace pods evenly across
          # nodes.
          pod_anti_affinity {
            preferred_during_scheduling_ignored_during_execution {
              weight = 1
              pod_affinity_term {
                topology_key = "kubernetes.io/hostname"
                label_selector {
                  match_expressions {
                    key      = "app.kubernetes.io/name"
                    operator = "In"
                    values   = ["coder-workspace"]
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}
