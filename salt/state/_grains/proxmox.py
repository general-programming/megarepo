"""Custom grain: detect Proxmox VE hosts."""

import os
import shutil


def proxmox():
    return {
        "proxmox": os.path.isdir("/etc/pve") or shutil.which("pveversion") is not None
    }
