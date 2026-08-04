# SN200 firmware availability — is anything newer than `KNGND122`?

Scope: HGST/WDC Ultrastar DC SN200 (SFF/U.2) and SN260 (HHHL card), ASIC
codename **Omaha**, PCI ID `1c58:0023`. Subject drives: five ×
`HUSMR7676BDP3Y1` (7.68 TB, WD SKU `0TS1357`), all currently on `KNGND122`.

Question: does a firmware revision newer than `KNGND122` exist anywhere —
generic, OEM, or archived — and would it close the shutdown/PFAIL/link-down
defect family?

Research date: **2026-08-04.**

---

## Verdict

**`KNGND122` is the last firmware. Nothing newer exists on any branch, at any
OEM, anywhere reachable. Confidence: high.**

The drives are already running it. There is **no firmware upgrade path and no
firmware fix to obtain.** Whatever is killing them, a flash will not help.

Three independent sweeps — OEM firmware catalogs, hardware-compatibility
matrices, and vendor/mirror/community archives — converged on the same ceiling.
The strongest single pieces of evidence:

1. **The OEM branches are lateral, never ahead.** Cisco's branch tops out at
   `KNCCD122` — same level `122` — and is still unchanged in *UCS C-Series
   Firmware Files 4.3(6)* (page updated 2025-11-13). Dell EMC's array branch
   tops out at `KNECD116`, i.e. **behind** 122. HPE and Lenovo never rebranded
   the drive at all.
2. **VMware's vSAN HCL, still republished in 2026,** lists all 20 SN200/SN260
   SKUs under partner "HGST, Inc." with exactly one certified firmware —
   `KNGND110` — through ESXi 9.1. No OEM-rebranded SN200 entry exists in the
   HCL from any vendor.
3. **Direct string probes for every plausible successor** (`KNGND123`, `124`,
   `125`, `130`, `131`, `140`, `KNGND2*`, `KNGNE*`, `KNCCD123`, `KNCCD130`)
   return **zero hits** in web search, GitHub code search across all of GitHub,
   vendor catalogs, or any archive. Confirmed independently here via
   `gh api search/code`.
4. **A curated third-party firmware archive still being maintained in 2025**
   (dm-cli 2.5, Cisco HUU entries dated 2024-08 and 2025-08) still tops out at
   `KNGND122`.

The corollary is that these drives are **not retirement candidates *because of*
missing firmware** — they are on the terminal image. The retire/keep decision has
to be made on the field evidence in `sn200-field-evidence.md` and the link-layer
analysis in `sn200-firmware-re.md`, not on a firmware upgrade that does not
exist.

---

## 1. The revision-string scheme, decoded

`KNGND122` is the 8-character NVMe **Firmware Revision** (`FR`, Identify
Controller offset `0x40`, length 8). Structure, evidenced from the four WD PDFs
in the local package, the in-image string tables, and the OEM sibling strings
found in the wild:

```
K   N   G N   D   1 2 2
|   |   \_/   |   \___/
|   |    |    |     └── 3-digit level, monotonic within the branch
|   |    |    └──────── branch/maturity letter: P (pre-prod) → D (production); Z on SBLs
|   |    └───────────── OEM / customer code
|   └────────────────── ASIC family
└────────────────────── vendor-brand letter
```

| Field | Observed values |
|---|---|
| char 1 — brand | `K` = HGST/WDC. `H` = Hitachi-branded (the `libdmi` predicate `hgst_nvmec_hitachi_block_point_chg_fw()` tests `FR[0]=='H'` — see `sn200-firmware-re.md` §11). |
| char 2 — ASIC family | `N` = **Omaha** (SN2x0). `M` = **GF** (SN100/SN150 — `KMGNP*`). `T` = WD IntelliFlash arrays (`KTGND76E`). |
| chars 3–4 — OEM code | `GN` generic HGST/WD · `CC` **Cisco UCS** · `EC`/`EG` **Dell EMC** arrays · `GW` unidentified |
| char 5 — branch | `P` pre-production · `D` production · `Z` secondary boot loader (`KNEGZ107`) |
| chars 6–8 — level | `040` … `122` |

Two hard confirmations of the family split, which is why cross-flashing is
forbidden: the SN150 string table header reads `### GF StringTable ###` and
every SN2x0 image reads `### Omaha StringTable ###`; `KMGNP*` and `KNGND*` are
**different silicon**.

### Complete evidenced revision list

| Revision | Branch | Evidence | Date | Confidence |
|---|---|---|---|---|
| `KNGNP040` | generic, pre-prod | `KNGNP100_SN2xx_Errata.pdf` | — | high |
| `KNGNP070` | generic, pre-prod | same | — | high |
| `KNGNP076` | generic, pre-prod | same | — | high |
| `KNGNP100` | generic, pre-prod | binary in local package | 2017-05-25 | high |
| `KNGND070` | generic | `KNGND100_SN2xx_Errata.pdf` | — | high |
| `KNGND090` | generic | same | — | high |
| `KNGND100` | generic | binary; live SMART dump (`linuxhw/SMART`, `HUSMR7619BHP3Y1`) | 2017-10-11 | high |
| `KNGND101` | generic | ServeTheHome operator reports; Dell Support ("third party firmware KNGND101") | — | high |
| `KNGND110` | generic | binary; **26 crowd-sourced SMART dumps**; VMware HCL | 2018-06-26 | high |
| `KNGND112` | generic | OEM branch cross-reference | — | medium |
| `KNGND113` | generic | OEM branch cross-reference | — | medium |
| **`KNGND122`** | generic | **binary + release notes; the ceiling** | build 2020-09-17, RN 2021-02-11 | high |
| `KNCCP100` | Cisco | Cisco UCS firmware listings | — | medium |
| `KNCCD101` | Cisco | Cisco UCS firmware listings | — | medium |
| `KNCCD111` | Cisco | still current for SN260 AIC PIDs `UCSB-F-H-5607`/`-H-32003` | — | high |
| **`KNCCD122`** | Cisco | UCS C-Series Firmware Files 4.1(3) → **4.3(6)** (page updated 2025-11-13); Intersight Managed Mode bundles 4.3/5.2/5.3/5.4 (2025-02-28); `scotttyso/intersight-tools` `firmware.json` | binary dated 2022-07-22 | high |
| `KNECD10B` | Dell EMC | field reports | — | low-medium |
| `KNECD112` | Dell EMC | field reports | — | low-medium |
| `KNECD116` | Dell EMC | field reports — **highest `KNEC*` seen, still below 122** | — | medium |
| `KNEGZ107` | Dell EMC | SBL image | — | low-medium |
| `KNGWD110` | unidentified OEM | one report, SKU `0TS1355`; reportedly **rejects** generic `KNGND` images | — | low |
| `KTGND76E` | WD IntelliFlash | WD array product | — | low-medium |

**No revision above level `122` exists on any branch.**

### Correction to a common premise

**`HUSMR3*` is not SN200.** Parts like `HUSMR3232ASS200` are **SAS** Ultrastar
SS200/SS300 SSDs (`ASS` = SAS), on a completely separate firmware line (Dell
`J172`, Cisco `A170`). Do not cross-apply anything from them. The NVMe family is
`HUSMR76*` (`BDP` = SN200 U.2, `BHP` = SN260 AIC); `HUSPR3*` is the SN100 AIC.

---

## 2. What `KNGND122` actually fixed

Extracted verbatim from `KNGND122_Release_Notes.pdf` (Western Digital
Confidential, dated **February 11, 2021**, "Initial Release. Changes from
KNGND110 to KNGND122"). The entries in the shutdown/PFAIL/link-down family, all
flagged **Drive Recovery: Unable to recover**:

- **Dual Port Shutdown** (High/Low) — *"When a shutdown is issued, internally the
  firmware will invoke a thread to monitor PFAIL (power fail) during shutdown.
  Due to a logic error in the firmware, if there is another shutdown triggered
  from the other port during this time, the PFAIL monitor thread is added again
  to the thread execution list. When this occurs, the pointers to the execution
  list becomes broken and a hang occurs during the shutdown process."*
- **Reset** (High/Low) — *"A race condition exists when a PCIE uncorrectable
  error occurs with a host link down that causes the Completion Queue messages
  to go into autodisable mode. The firmware timeouts waiting for the response
  from the hardware and leads to a drive hang."*
- **Link Error Handling** (Medium/Low) — hang when a host link-down coincides
  with a PCIe error; the host interface enters auto-disable and the firmware
  re-enables only the offending queue engine instead of the whole host interface.
- **Reset** (High/Low, *Drive Recovery: Power Cycle Required*) — the same
  link-down + doorbell-error race.
- **Shutdown** (High/Medium) — a Format-with-Secure-Erase interrupted by an NVMe
  shutdown crashes the drive.
- **FW Update** (High/Medium) — an out-of-bounds offset in FW download hangs the
  drive.

So `KNGND122` is the release that **closed** the PFAIL-monitor-thread bug. It is
the terminal state of that campaign, and the PDF contains no forward reference to
any later revision.

**Upgrade requirements** from the same notes, relevant only if a drive is found
*below* 122: firmware download (`11h`), commit (`10h`), select the slot, then a
Controller Reset / NVMe Subsystem Reset / PERST# / power cycle **is required**
when coming from `KNGND110`. Controller Reset **cannot** activate firmware in a
dual-port configuration. *"If upgrading from firmware KNGND100 or older, please
contact WDC as different upgrade instructions may apply."*

**No OEM ever issued an advisory for this defect.** Cisco field notices FN74253
and FN72225 were read in full — Intel drives only. HPE's NVMe bulletins
(`a00111900`, `a00112800`, `a00092491`) name no HGST/WD part. The defect family
is documented **exclusively** by WD, in the release notes shipped inside the
firmware zip.

---

## 3. Official WD / SanDisk channels today

| Check | Result | Confidence |
|---|---|---|
| `westerndigital.com/support/hgst/data-center-drives/ssd/ultrastar-sn200-series` | **HTTP 404** | high (fetched) |
| `westerndigital.com/support/wdc/…/ultrastar-sn200-series` | 301 → `shop.sandisk.com` → storefront, **no firmware section** | high (fetched) |
| `sandisk.com/en-gb/support/wdc/…/ultrastar-sn200-series` | storefront only, no firmware, no revision strings | high (fetched) |
| `link.westerndigital.com/enterprisesupport/software-download.html` | **HTTP 404** — the enterprise download portal that generated `SanDisk-bundle-*.zip` is gone | high (fetched) |
| WD SN200 compatibility PDF (`documents.westerndigital.com/…/compatibility-ultrastar-Sn200-series-ssds.pdf`) | live URL 404; 2021-01-08 Wayback capture recovered and text-extracted — **contains no firmware revision strings at all** | high |
| Wayback: 26 captures of the WD SN200 support page, 2019-08-21 → 2025-11-09 | **not a single `KNGN*` string in any snapshot** — WD never listed firmware versions publicly; downloads were always portal-gated | high |
| `portal.wdc.com/s`, `support-en.hgst.com` | login-walled; per operator reports the registration-validation step blocks individuals | high |
| Web + GitHub-wide search for `KNGND122` | **one** GitHub hit, zero public download pages; the release-note PDF is marked *Western Digital Confidential* | high |

WD's live firmware KB
([`a_id/50745`](https://support-en.wd.com/app/answers/detailweb/a_id/50745/~/firmware-download-and-updates-for-western-digital-internal-and-external-drives))
states the policy outright, verbatim:

> Western Digital and HGST Ultrastar drives have the final firmware installed at
> the build process normally. There may be an update for some drives that are in
> warranty. **Drives out of warranty are not supported.**

In-warranty owners must open a support case with model, current firmware version
and a photo of the drive label; there is no self-service download. A 2017-era
SN200 is far out of warranty, so this route is closed.

### The portal lag is itself evidence — and a caveat

The local package contains `SanDisk-bundle-6149148d4706a.zip`, generated by WD's
own portal on **2021-09-20**, seven months *after* the `KNGND122` release notes.
Its `Linux/Current/Firmware/` folder contains only **`KNGNP100` and
`KNGND100`**, and its documentation tops out at the `KNGND110` release notes.

Contrast the sibling SN150 bundle (`SanDisk-bundle-614b55d5b99e8.zip`, generated
2021-09-22), which has a properly maintained versioned tree: `1.1.0/`
(`KMGNP110`), `1.2.0/` (`R1.20`), `1.3.1/` (`KMGNP131`).

**The SN200 branch of WD's public portal was abandoned around 2018.** So the
absence of a `KNGND130` from public channels would, on its own, prove nothing —
only the enterprise channel would show it. That is exactly why the OEM catalogs
and the Cisco branch matter: **Cisco's independently maintained branch, still
being republished in November 2025, also stops at level `122`.** That is what
converts "not published publicly" into "does not exist."

---

## 4. OEM sweep — per vendor

| Vendor | Result | Confidence |
|---|---|---|
| **Cisco UCS** | The only substantive OEM branch. `KNCCD122` is terminal; first in HUU 4.1(3), unchanged in *UCS C-Series Firmware Files 4.3(6)* (updated 2025-11-13) and Intersight Managed Mode bundles 4.3/5.2/5.3/5.4 (2025-02-28). Covers `UCSC-NVME-H76801/-H38401/-H32003/-H64003`, `UCSC-/UCSB-NVMEHW-*`, `UCS-S3260-NVM4*`. SN260 AIC PIDs `UCSB-F-H-5607`/`-H-32003` are still on the *older* `KNCCD111`. UCS Firmware Files 6.0 dropped HGST entirely. | high |
| **Dell** | **No NVMe firmware DUP for SN200/SN260 has ever existed.** `downloads.dell.com/catalog/Catalog.gz` + `Catalog.xml.gz` + `ASHCI-Catalog.xml.gz` (~40 MB XML) fetched and grepped: **zero `KNGN*`, zero `HUSMR76*`**. Every "Express Flash NVMe" DUP is Samsung/Intel/Kioxia/Micron. Dell Support's written position: *"third party… running on third party firmware KNGND101"* — they route customers to WD. Dell **server** units run stock `KNGND*`; Dell **EMC array** units run the separate `KNEC*` branch topping out at `KNECD116`, *below* 122. | high |
| **HPE** | Never rebranded it. Four SPP contents reports (Gen10 2020.09 / 2021.04 / 2022.03, Gen9 2021.10.1) text-extracted in full — verbatim counts across all four: `HUSMR` = 0, `HGST` = 0, `Western Digital` = 0, `KNGND` = 0, `SN200` = 0. HPE's ~300 NVMe part numbers are Samsung/Intel/Kioxia/Micron on an unrelated `HPKn`/`GPKn` scheme. `MO007600KWZQD` does not exist; the real `KWZQ` family is Samsung at `HPK5`. HPE's Gen10 firmware repo has exactly one WDC component, `WDC_ParisD_WparisdASFD1.fwpkg` (Paris SATA family). | high |
| **Lenovo** | Never rebranded it. *ThinkSystem SSD Portfolio Comparison Reference* (lp1261) contains **no SN200, no SN260, no HUSMR**; the only HGST/WD entries are **SAS** SS300/SS530 plus the much later SanDisk SN861. Corroborated by lp1059, lp2256, ServerProven for SR650. | high |
| **NetApp, Hitachi Vantara, Huawei, Inspur, Quanta/QCT, Supermicro, Oracle, Fujitsu, IBM** | All negative. QCT lists SN260 as an option but publishes no drive firmware; Supermicro distributes none; Oracle's NVMe SKUs are Intel; NetApp never OEM'd the SN200. | medium-high |

---

## 5. Compatibility matrices as a version oracle

| Oracle | Result | Confidence |
|---|---|---|
| **VMware / Broadcom vSAN HCL** (`partnerweb.vmware.com/service/vsan/all.json`, 20 MB, live, snapshot 2026-07-30) | All **20** SN200/SN260 SKUs sit under partner "HGST, Inc." only — **no OEM-rebranded SN200 entry from any vendor**. Sole certified firmware ESXi 6.x → **9.1** is `KNGND110`, repeated ~280 times. VMware never even qualified `KNGND122`. Across all 6 858 SSD entries the only `K?GN*`-scheme firmware belongs to those 20 HGST parts. | very high |
| **Crowd-sourced SMART databases** | 27 real drives, **none above `KNGND110`**: `linuxhw/EnterpriseDrive` `HUSMR7638BDP3Y1` ×24 → `KNGND110` (verified here: `gh api search/code` returns 24 `KNGND` hits in that repo, all 110); `bsdhw/SMART` `HUSMR7632BDP301` ×2 → `KNGND110`; `linuxhw/SMART` `HUSMR7619BHP3Y1` → `KNGND100`. | high |
| **GitHub-wide code search** (verified here) | `KNGND122` → 1 hit. `KNGND123`, `KNGND125`, `KNGND130`, `KNGND131`, `KNGND140`, `KNGNE100` → **0 hits each**. | high |
| **LVFS / fwupd** | No WDC/HGST enterprise NVMe entries. WD does not ship SN200 firmware through LVFS. | high |
| **smartmontools `drivedb.h`** (master, fetched and grepped) | zero `HUSMR` / `KNGN` matches. | high |
| **NetApp IMT, Nutanix LCM, Red Hat/Oracle hardware certification** | nothing; portals auth-gated or no SN200 coverage. | medium |
| **WD "Partner Product Compatibility" / "Product Compatibility" pages** | dynamic selector UI, no firmware data. | high |
| **archive.org** item/full-text search for `SN200`, `KNGND`, `HUSMR`, `Ultrastar NVMe` (queried directly) | no firmware items. | high |

---

## 6. Community and field evidence

- **ServeTheHome**, ["The quest for the HGST UltraStar SN260 firmware updates…"](https://forums.servethehome.com/index.php?threads/the-quest-for-the-hgst-ultrastar-sn260-firmware-updates.34135/)
  — 4 pages, the definitive thread. Ceiling is `KNGND122`. Documents
  `nvme fw-download -f KNGND122.bin` and the
  `hdm manage-firmware -u <serial> --load -f .\KNCCD122_padded.bin --slot 5 --activate --reset`
  workflow, plus `dm-cli --clear-diag-data` rescuing ~30 Dell SN200s from
  diagnostic mode — with the warning that it **resets sector size to 4096** from
  Dell's 520 and **zeroes TBW/POH SMART counters**. Trust: medium-high.
- **ServeTheHome**, ["HGST HUSMR7676BHP3Y1 firmware? (SN200 7.68Tb)"](https://forums.servethehome.com/index.php?threads/hgst-husmr7676bhp3y1-firmware-sn200-7-68tb.34074/)
  (Sept 2021). Poster had **Dell-branded** SN200s that "ran an HGST firmware" —
  no Dell-specific branch. Shipped on `KNGND101`; `KNGND122` fixed an iDRAC
  5.00.yy.z boot hang. Corroborated on
  [Dell's own forum](https://www.dell.com/community/en/conversations/rack-servers/dell-r540-idrac-5-upgrade-will-cause-server-hang/647f99d4f4ccf8a8ded4d7e2)
  (April 2023): *"those drives work fine on iDRAC 5.y+ if you have firmware
  v122."* Trust: high.
- **Broadcom/VMware community**, ["SSD flash HGST-Ultrastar-SN260-NVMe problem"](https://community.broadcom.com/vmware-cloud-foundation/discussion/ssd-flash-hgst-ultrastar-sn260-nvme-problem)
  — names `KNGND101` and `KNGND110`; a Sept 2021 poster asks *"Does anyone know
  where to find the newer firmware? KNGND110"* and gets **no answer**. Trust: medium.
- **Level1Techs**, ["HGST/WDC Ultrastar SN200 Recovery from Persistent Internal Error / Diagnostic State"](https://forum.level1techs.com/t/hgst-wdc-ultrastar-sn200-recovery-from-persistent-internal-error-diagnostic-state/250303)
  — operator on `KNGND110` tried and **failed** to load `KNGND122.bin` via the
  HDM workflow. **The actual fix was activating a different existing firmware
  slot** (slots 2/3/4), not a new image.
  → *Corrected since:* the bundle format is **not** why HDM refused it. WD's own
  `nvmec_fw_img_dl` never parses the file — it ships every byte verbatim at
  dword offset 0. Whatever rejected it was host-side (or the read-only-slot
  refusal, misread), and `nvme fw-download` bypasses it entirely. See
  `docs/sn200-firmware-flashing.md` §2.
  → Worth reading `nvme fw-log` on all five drives before writing any of them
  off. Trust: medium-high.
- **virtualbytes.io**, ["VMware Cloud Foundation 9.x: Fixing WD HGST Ultrastar DC SN200 NVMe Drives Stuck in Diagnostic Mode"](https://virtualbytes.io/vmware-cloud-foundation-9-x-fixing-wd-hgst-ultrastar-dc-sn200-nvme-drives-stuck-in-diagnostic-mode-orange-led-blinking/),
  **2025-12-10**. Someone hitting this exact failure five years after
  `KNGND122`; the remedy offered is `dm-cli capture-diagnostics --clear`, **not
  a firmware update**, and no firmware version or download is named anywhere.
  Strong circumstantial evidence that no fix release exists. Trust: medium-high.
- **Win-Raid** (`www.win-raid.com` offline; migrated to
  [winraid.level1techs.com](https://winraid.level1techs.com/t/windows-nvme-driver-for-hgst-ultrastar-dc-sn200-sas-nvme-ssd/33320))
  — drivers only, **no** `KNGND`/`sblpatch` strings anywhere.
- **Reddit / r/DataHoarder / TrueNAS forums / Chinese mirrors (CSDN, Baidu,
  Gitee) / GitHub repo search** — nothing. Trust: medium.

---

## 7. Where firmware is obtainable today

You already hold everything. For completeness, and only as a fallback if a drive
turns up below `KNGND122`:

| Source | Status | Trust |
|---|---|---|
| `~/Downloads/HGST-UltraStar-SN200-HHHL.zip` (local) | contains `KNGND100`, `KNGND110`, `KNGND122` binaries + all four WD PDFs | contents are verbatim WD portal artifacts; **treat as the reference copy** |
| Dropbox share linked from the ServeTheHome thread (`dropbox.com/sh/l83w0z72psdylrj/…`) | live, unauthenticated, `HGST.zip` ≈ 156 MB; the same SN200 zip plus `HGST-UltraStar-SN200-HHHL-Cisco.zip` (`KNCCD122.bin` / `_padded` / `.encrypted`, dated 2022-07-22), `dm-2.5-win64.zip`, `dm-core-2.5.1-7.x86_64.rpm`, `DM-CLI_User_Guide.pdf` | **third-party mirror — low trust as a *source*, but the payload matches the local copy byte-for-byte** (see hashes below), which is what makes it usable |
| `deb.digdeo.fr/firmware/HGST/` | **HTTP 401** since at least 2026-08-04; and per the 2021-04-14 Wayback capture the tree only ever held `HUC109090CSS60/` (a SAS HDD) and `utils/` — **never any SN200 firmware**. Archived Hugo/Niagara/HGST-Device-Manager binaries remain fetchable via Wayback. | mirror, tools only |
| `ftp.abacus.cz/distribuce/HGST/` | reachable and indexed: `Crypto Erase/`, `Drive Fitness Test (DFT)/`, `HGST Device Manager/`, `HiTest/`, `Hugo/`, `Niagara/` — **tools only, no SSD firmware** | mirror, tools only |
| WD/SanDisk official | nothing — see §3 | — |

### Verified hashes (computed locally, 2026-08-04)

```
b11298346020af0f3a859e5a0d849c464eed186c9a102cf8956b3f6c44db3e70  KNGND122.bin              1762048 B  2020-09-17
7210283c62ef88b08ace950fa53203f97d0dc34957ecab3b43fd565c758ccff2  KNGND110.bin              2009856 B  2018-06-26
7210283c62ef88b08ace950fa53203f97d0dc34957ecab3b43fd565c758ccff2  KNGND110+sblpatch+k.bin   2009856 B  2018-06-26
134d67c992f8938a59b67ce0a1788bf04fddf3dd5b56fe8a8897c2b518203309  KNGND100.bin              1680128 B  2017-10-11
```

Use these to authenticate anything pulled from a third-party mirror. A firmware
image from an unverified source that does **not** match one of these hashes is a
brick risk and must not be flashed.

---

## 8. Safety flags

- **⚠ The only `KNGND110` image in existence in this package is the
  `+sblpatch+k` variant.** `firmwares/KNGND110.bin` and
  `KNGND110+sblpatch+k.zip → .bin` are **byte-identical** (both
  `7210283c…ccff2`, both 2 009 856 B — verified locally). There is no separate
  "plain" `KNGND110`. Per prior RE, `+sblpatch+k` writes **every** slot including
  the secondary boot loader, destroying the fallback, and WD does not support
  downgrade once the SBL is updated. **Do not flash it**, and do not be misled by
  the innocuous filename in `firmwares/`.
- **Never cross-flash `KMGNP*` (SN150/SN100, "GF") onto an SN2x0 ("Omaha").**
  Different ASIC, different string table, different image layout.
- **Never cross-flash across OEM branches.** A drive whose `FR` starts `KNCC`,
  `KNEC`, `KNEG`, `KNGW` or `KTGN` is on an OEM branch and will **reject**
  `KNGND*.bin` with `Device firmware version is not compatible with this
  operation`. This is a clean refusal, not a brick — but do not attempt to force
  it. Conversely, chasing a Cisco HUU ISO to extract `KNCCD122` is **pointless**
  for these generic `KNGND` drives: identical revision level, no additional fix.
- Dell EMC array drives on the `KNEC*` branch are **stuck below the fix level**
  (`KNECD116` < `122`), with no publicly surfaced `122`-level `KNEC` image. Not
  applicable to these five drives, which are generic.
- The five subject drives are already on `KNGND122`. **There is nothing to flash
  up to.** Any flash operation on them is pure risk with zero expected benefit.

---

## 9. What this means for the five drives

Firmware is a closed question. The decision now rests on evidence gathered
elsewhere:

1. **Read `nvme fw-log` on all five.** The Level1Techs case shows a drive can be
   recovered by activating a different slot; and it confirms the drives ship with
   multiple populated slots. Verify each drive is genuinely running `KNGND122`
   in the active slot before concluding anything.
2. `sn200-field-evidence.md` already records that on the one drive analysed in
   depth, the U.2 cable for that bay is known-flaky and the host logged
   `UEFI0067: A PCIe link training failure … the link is disabled`. A drive
   absent from `lspci` is a **cabling fault, not a firmware fault**, and no
   firmware would have fixed it.
3. `sn200-nondestructive-recovery.md` and the `nvme-recovery` skill hold the
   non-destructive recovery ladder, including `dm-cli --clear-diag-data`. Note
   the STH warning that it resets sector size to 4096 and zeroes TBW/POH.

Retirement, if chosen, should be justified by the failure rate and the absence of
vendor support — **not** by "there is a firmware fix we cannot get." There is not.

---

## 10. Sources

- Local package `~/Downloads/HGST-UltraStar-SN200-HHHL.zip` —
  `KNGND122_Release_Notes.pdf`, `KNGND110_Release_Notes_v2.pdf`,
  `KNGND100_SN2xx_Errata.pdf`, `KNGNP100_SN2xx_Errata.pdf`,
  `SanDisk-bundle-6149148d4706a.zip`, `WD-SN200-30190204-readme.txt`,
  `README.txt`, `URL.txt`.
- Prior RE in this repo: `docs/sn200-firmware-re.md`,
  `docs/sn200-independent-re.md`, `docs/sn200-field-evidence.md`,
  `docs/sn200-nondestructive-recovery.md`, `docs/sn200-crash-dump-retrieval.md`,
  `.claude/skills/nvme-recovery/SKILL.md`.
- [WD firmware policy KB `a_id/50745`](https://support-en.wd.com/app/answers/detailweb/a_id/50745/~/firmware-download-and-updates-for-western-digital-internal-and-external-drives)
- [Broadcom vSAN/SSD Compatibility Guide](https://compatibilityguide.broadcom.com/detail?program=ssd&productId=42269)
  and `partnerweb.vmware.com/service/vsan/all.json`
- Cisco *UCS C-Series Firmware Files 4.3(6)*; Intersight Managed Mode Release
  Bundle 4.3/5.2/5.3/5.4; `scotttyso/intersight-tools` `firmware.json`
- `downloads.dell.com/catalog/Catalog.gz`, `Catalog.xml.gz`, `ASHCI-Catalog.xml.gz`
- HPE SPP contents reports (Gen10 2020.09/2021.04/2022.03, Gen9 2021.10.1)
- Lenovo `lp1261`, `lp1059`, `lp2256`, ServerProven SR650
- `linuxhw/SMART`, `linuxhw/EnterpriseDrive`, `bsdhw/SMART`; GitHub code search
- Forum and blog sources cited inline in §6.
