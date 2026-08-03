"""Vendor-command encodings for the HGST/WDC Ultrastar SN200 (Omaha) NVMe SSD.

Single source of truth for both `pull-crash-dump.sh` and the decoder. Every
encoding here is PROVEN from three independent sources that agree exactly:

  1. `libdmi_core.so.0.39` (WD Device Manager 2.5.1) -- `gf_nvme_vuc_simple_real`
     @ 0x8bf90 builds the raw 64-byte SQE:
         cmd[0x00] = opcode;  cmd[0x28] = cdw10;  cmd[0x30] = (subcmd<<8)|cmd_id
     everything else zero, so NSID = 0.
  2. `nvme-cli` plugins/wdc/wdc-nvme.c -- the WDC_NVME_* defines and
     `wdc_do_dump()` / `wdc_dump_length()`.
  3. The firmware's own `VUC Get Drive Log SubCmd %08X` dispatcher in PROC8's
     overlay bank @ 0x30030d14, where subcmds 3 and 5 (the two size probes)
     visibly share one handler.

READ-ONLY. Nothing in this module emits a command that writes to the drive.
"""

VUC_OPCODE = 0xC6  # "capture diagnostics command" opcode
NSID = 0

# CDW12 = (subcmd << 8) | cmd_id.  cmd_id 0x20 is the "Get Drive Log" family.
CMD_GET_DRIVE_LOG = 0x20

SUB_DRVLOG_BODY = 0x00
SUB_DRVLOG_SIZE = 0x01
SUB_STRTBL_BODY = 0x02
SUB_STRTBL_SIZE = 0x01  # same probe as DRVLOG_SIZE; size lands in dword[1]
SUB_CRASH_SIZE = 0x03
SUB_CRASH_BODY = 0x04
SUB_PFAIL_SIZE = 0x05
SUB_PFAIL_BODY = 0x06


def cdw12(subcmd: int, cmd_id: int = CMD_GET_DRIVE_LOG) -> int:
    return ((subcmd & 0xFF) << 8) | (cmd_id & 0xFF)


# Section descriptor: the four blobs `dm-cli` pulls, named by the same 8-char
# tags the firmware uses in its E6 manifest at PROC8:0x7ff80570.
#   size_dword: which u32 of the 8-byte size-probe reply carries this size.
SECTIONS = {
    "crash": dict(
        tag="CRSHDMP ",
        size_cdw12=cdw12(SUB_CRASH_SIZE),  # 0x0320
        body_cdw12=cdw12(SUB_CRASH_BODY),  # 0x0420
        size_dword=0,
    ),
    "pfail": dict(
        tag="PFCRDMP ",
        size_cdw12=cdw12(SUB_PFAIL_SIZE),  # 0x0520
        body_cdw12=cdw12(SUB_PFAIL_BODY),  # 0x0620
        size_dword=0,
    ),
    "strtbl": dict(
        tag="STRTBL  ",
        size_cdw12=cdw12(SUB_STRTBL_SIZE),  # 0x0120
        body_cdw12=cdw12(SUB_STRTBL_BODY),  # 0x0220
        size_dword=1,
    ),
    "drvlog": dict(
        tag="DRVLOG  ",
        size_cdw12=cdw12(SUB_DRVLOG_SIZE),  # 0x0120
        body_cdw12=cdw12(SUB_DRVLOG_BODY),  # 0x0020
        size_dword=0,
    ),
}

# The body read's byte offset is carried in CDW13 as a DWORD count.
# PROVEN by nvme-cli `wdc_do_dump()`:
#     admin_cmd.cdw10 = curr_data_len >> 2;
#     admin_cmd.cdw13 = curr_data_offset >> 2;
# libdmi_core never chunks (it mallocs the whole size and issues one command),
# so it leaves CDW13 at zero -- consistent, not contradictory.
OFFSET_CDW = 13

# Alternate offset register. WD's own library uses CDW11 for the *0xE6* dump
# (`hgst_nvme_log_dump_real` @ 0x8c4f0 writes arg3 to cmd[0x2c]); nvme-cli uses
# CDW13 for 0xE6 as well. They disagree for 0xE6, so treat CDW11 as the
# documented fallback if the CDW13 probe shows the offset being ignored.
OFFSET_CDW_ALT = 11

# --- Commands this tooling must never emit -----------------------------------
# 0xFF/CDW12=0x0503 clear crash dump  -> schedules Drive REINIT -> WIPES the
#                                        namespace on the next startup.
# 0xFF/CDW12=0x0603 clear pfail dump  -> erases the pfail section immediately.
# 0xFF/CDW12=0x0303 erase SBL EEPROM  -> permanent brick.
# 0xFF/CDW12=0x0403 drive uninit      -> destroys the drive's provisioning.
# 0xDD              secure purge      -> irreversible full wipe, no confirmation.
# `nvme wdc get-crash-dump` fires 0x0503/0x0603 automatically after a successful
# read (nvme-cli `wdc_do_crash_dump()` -> `wdc_do_clear_dump()`). Never use it.
FORBIDDEN_CDW12 = {0x0503, 0x0603, 0x0303, 0x0403}
FORBIDDEN_OPCODES = {0xFF, 0xDD, 0xD8, 0xD9}
