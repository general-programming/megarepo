# Nix Kubernetes workspace template

Provisions a Coder workspace as a Kubernetes Deployment, based on
[`coder/registry`'s kubernetes example](https://github.com/coder/registry/tree/main/registry/coder/templates/kubernetes),
with a persistent, per-workspace Nix store instead of a stock devcontainer
image.

## Architecture

- **No code-server / browser IDE.** Access is via `coder ssh` or an editor's
  Remote-SSH integration.
- **Single-user, no-daemon Nix**, running as a non-root uid 1000, matching a
  non-privileged pod `security_context`. The workspace image
  (`image/Dockerfile`) installs Nix via the traditional `nixos.org/nix/install`
  script with `--no-daemon` — the Determinate Systems installer has no
  equivalent single-user mode, so it isn't used here.
- **`/nix` is persisted** on the same PVC as `/home/coder`, mounted a second
  time at `/nix` via `sub_path = "_nix"` — i.e. `/home/coder/_nix` on disk is
  bind-mounted to `/nix` in the container. This means Nix packages, flakes,
  and profiles survive a workspace stop/start the same way the home
  directory does.
- Both mounts are empty the first time a workspace's PVC is created, so an
  **initContainer** (`seed-nix`, same image) copies the image's own
  pre-installed `/nix` into the persisted volume on first boot only, gated on
  a `.seed-complete` marker file (not "is the directory empty", which would
  permanently skip re-seeding after a crash mid-copy).
- Nix's default build sandbox needs unprivileged user namespaces, which Talos
  blocks cluster-wide at the kernel level (`user.max_user_namespaces = 0`).
  Rather than requesting a cluster-wide sysctl change for this template, the
  image ships `sandbox = false` in `/etc/nix/nix.conf`.
- Because `/home/coder` is also an empty persisted volume at runtime, the
  `~/.nix-profile` shell integration the installer wrote at image-build time
  doesn't survive a real boot. The image instead bakes a system-wide
  `/etc/profile.d/nix.sh` that sources Nix from a fixed path under `/nix`
  (`/nix/var/nix/profiles/per-user/coder/profile/...`), so every shell
  (agent startup script, `coder ssh`, interactive login) picks it up
  automatically.
- Optional one-time dotfiles bootstrap via the `dotfiles_uri` parameter
  (`coder dotfiles`), gated on a marker file in `$HOME`. Nothing personal is
  baked into the image itself.

## Personalization parameters

Workspaces can bootstrap themselves from a Nix flake with Home Manager:

| Parameter | Example | What it does |
|---|---|---|
| `repo_url` | `https://github.com/nepeat/nepeat` | Cloned into `$HOME/<repo>` on first boot |
| `flake_dir` | `nix` | Repo-relative dir containing `flake.nix` (blank = repo root) |
| `hm_config` | `erin` | `homeConfigurations.<name>` activated on first boot |
| `home_user` | `erin` | Username + `/home/<name>` (blank = Coder username; immutable) |

How it hangs together:

- **`home_user`**: the home PVC is mounted at `/home/<home_user>` and the
  container gets explicit `HOME`/`USER` env (Kubernetes would otherwise
  derive `HOME` from the image's passwd entry, `/home/coder`). On every boot
  the startup script rewrites `/etc/passwd`, `/etc/group`, and sudoers so
  uid 1000 is *named* `home_user` too — `/etc` is ephemeral image overlay,
  which is also why it must re-run each boot. `sed` instead of `usermod`
  because usermod refuses to rename a user with running processes (the
  agent). The uid stays 1000, so PVC file ownership is unaffected. This
  matters because Home Manager activation asserts `home.username == $USER`
  and `home.homeDirectory == $HOME`.
- **`repo_url` + `flake_dir` + `hm_config`**: first boot clones the repo
  into the persisted home, then runs
  `nix run home-manager -- switch -b hm-bak --flake $HOME/<repo>/<flake_dir>#<hm_config>`
  (gated on a `~/.hm-activated` marker; `-b hm-bak` backs up conflicting
  dotfiles instead of aborting). Activating from the local checkout — not a
  `github:` flake ref — means config edits made inside the workspace apply
  with a plain `home-manager switch`. The flake ref is the *directory*
  containing `flake.nix`, which is why `flake_dir` is a repo-relative path
  rather than a URL to the file.

## Prerequisites

- **Cluster**: an existing Kubernetes cluster. Provisions into the
  `coder-workspaces` namespace by default (see
  `argocd/apps/infra/namespace/base/coder-workspaces.yaml`) — must exist
  before workspaces are created.
- **Image**: `image/Dockerfile` is built and pushed to
  `ghcr.io/general-programming/megarepo/coder-nix-workspace` weekly by
  `.github/workflows/coder-nix-workspace-docker.yaml`. Trigger it manually
  (`workflow_dispatch`) if you need it built sooner than the next scheduled
  run.
- **Authentication**: uses in-cluster auth by default (`use_kubeconfig =
  false`), matching the Coder control plane already running as a pod on the
  same cluster (`argocd/apps/core-services/coder`).

## Pushing the template

The `coder` CLI is available via this repo's `devenv` shell:

```sh
coder templates push nix -d infrastructure/coder/nix-template
```
