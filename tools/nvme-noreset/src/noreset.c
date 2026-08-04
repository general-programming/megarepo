// SPDX-License-Identifier: GPL-2.0
/*
 * nvme-noreset: opt-in, PCI-ID-scoped workarounds for NVMe controllers whose
 * firmware raises a "persistent internal error" AEN in a loop.
 *
 * Motivating hardware: HGST/WDC Ultrastar SN200 (HUSMR7676BDP3Y1, 1c58:0023)
 * in "Post Crash Startup" diagnostic mode. It raises an Error-class AEN with
 * subtype 03h (persistent internal error) every ~5s; the stock driver resets
 * the controller each time, which the firmware records as another unexpected
 * start, re-arming the crash state and capping every admin command at a ~5s
 * window.
 *
 * BOTH knobs are empty by default. With no module parameters set, the driver
 * behaves exactly like stock.
 */

#include <linux/kernel.h>
#include <linux/ctype.h>
#include <linux/device.h>
#include <linux/moduleparam.h>
#include <linux/string.h>

#ifdef CONFIG_PCI
#include <linux/pci.h>
#endif

#include "noreset.h"

static char *persist_err_noreset_ids;
module_param(persist_err_noreset_ids, charp, 0444);
MODULE_PARM_DESC(persist_err_noreset_ids,
	"Comma-separated allow-list of PCI devices for which a persistent "
	"internal error AEN must NOT reset the controller. Entries are either "
	"vendor:device (\"1c58:0023\") or a PCI address (\"0000:b2:00.0\"). "
	"Empty (default) = stock behaviour for every device.");

static char *zero_discard_ids;
module_param(zero_discard_ids, charp, 0444);
MODULE_PARM_DESC(zero_discard_ids,
	"Comma-separated allow-list of PCI devices whose namespaces are set up "
	"with discard disabled (max_hw_discard_sectors = 0) at probe time. "
	"Same entry syntax as persist_err_noreset_ids. "
	"Empty (default) = stock behaviour for every device.");

/*
 * The low-level driver (nvme_pci.c, in nvme.ko -- not part of this module)
 * sets ctrl->max_hw_sectors from min(NVME_MAX_KB_SZ << 1, dma_opt_mapping_size()
 * >> 9) at probe time, *before* nvme-core's own nvme_init_identify() ever
 * runs. On hardware reporting mdts == 0 (unbounded), nvme-core's mdts
 * combination (min_not_zero with UINT_MAX) never lowers that value -- but it
 * never raises it either. In practice dma_opt_mapping_size() is an IOMMU
 * "optimal", not "maximum", mapping-size hint (iova_rcache_range(), 32 *
 * PAGE_SIZE on a 4K-page host = 128 KiB), which is what actually caps a
 * single admin passthrough command at 256 sectors.
 *
 * NVME_NORESET_ADMIN_MAX_SECTORS raises the ADMIN QUEUE's ceiling only, for
 * allow-listed devices, comfortably past a 3.2 MiB single-shot transfer,
 * while staying well under the low-level driver's own hard maximum
 * (NVME_MAX_KB_SZ << 1 == 16 MiB) -- so this value has never been rejected
 * by anything upstream of nvme-core. It is applied in nvme_set_ctrl_limits()
 * only when is_admin is true, so namespace I/O queues (and therefore normal
 * read/write sizing) are completely unaffected. The number of DMA segments
 * this can actually use is still clamped against ctrl->max_segments -- the
 * low-level driver's real, fixed-size scatterlist allocation -- so this can
 * never ask the transport to build more segments than it has memory for.
 */
#define NVME_NORESET_ADMIN_MAX_SECTORS 8192U /* 4 MiB */

static char *max_admin_xfer_ids;
module_param(max_admin_xfer_ids, charp, 0444);
MODULE_PARM_DESC(max_admin_xfer_ids,
	"Comma-separated allow-list of PCI devices whose ADMIN QUEUE ONLY gets "
	"a raised max_hw_sectors/max_segments ceiling (8192 sectors = 4 MiB), "
	"so a single large vendor admin command can be issued without "
	"chunking. Still hard-clamped against the low-level driver's real "
	"segment allocation (ctrl->max_segments) -- this cannot exceed what "
	"the transport can actually DMA-map. Namespace I/O queues are "
	"completely unaffected. Same entry syntax as persist_err_noreset_ids. "
	"Empty (default) = stock behaviour for every device.");

/* Compare one allow-list token against a device. Returns true on match. */
static bool nvme_noreset_token_matches(const char *tok, size_t len,
				       struct device *dev)
{
	char buf[24];

	if (!len || len >= sizeof(buf))
		return false;

	memcpy(buf, tok, len);
	buf[len] = '\0';

	/* "0000:b2:00.0" or "b2:00.0" -- match against the bus id. */
	if (strchr(buf, '.')) {
		const char *name = dev_name(dev);
		size_t nlen;

		if (!name)
			return false;
		if (!strcasecmp(buf, name))
			return true;

		/* Allow the domain to be omitted. */
		nlen = strlen(name);
		if (nlen > len && name[nlen - len - 1] == ':' &&
		    !strcasecmp(buf, name + nlen - len))
			return true;
		return false;
	}

#ifdef CONFIG_PCI
	/* "1c58:0023" -- vendor:device. */
	{
		unsigned int vid, did;
		struct pci_dev *pdev;

		if (sscanf(buf, "%x:%x", &vid, &did) != 2)
			return false;
		if (vid > 0xffff || did > 0xffff)
			return false;
		if (!dev_is_pci(dev))
			return false;
		pdev = to_pci_dev(dev);
		return pdev->vendor == vid && pdev->device == did;
	}
#else
	return false;
#endif
}

static bool nvme_noreset_list_matches(const char *list, struct device *dev)
{
	const char *p;

	if (!list || !*list || !dev)
		return false;

	for (p = list; *p; ) {
		const char *end;

		while (*p == ',' || *p == ' ')
			p++;
		if (!*p)
			break;
		end = strchr(p, ',');
		if (!end)
			end = p + strlen(p);
		if (nvme_noreset_token_matches(p, end - p, dev))
			return true;
		p = end;
	}
	return false;
}

bool nvme_noreset_suppress_reset(struct device *dev)
{
	return nvme_noreset_list_matches(persist_err_noreset_ids, dev);
}

bool nvme_noreset_zero_discard(struct device *dev)
{
	return nvme_noreset_list_matches(zero_discard_ids, dev);
}

u32 nvme_noreset_max_admin_sectors(struct device *dev)
{
	if (nvme_noreset_list_matches(max_admin_xfer_ids, dev))
		return NVME_NORESET_ADMIN_MAX_SECTORS;
	return 0;
}

void nvme_noreset_init(void)
{
	pr_info("nvme-noreset: patched nvme-core active (persist_err_noreset_ids=\"%s\" zero_discard_ids=\"%s\" max_admin_xfer_ids=\"%s\")\n",
		persist_err_noreset_ids ? persist_err_noreset_ids : "",
		zero_discard_ids ? zero_discard_ids : "",
		max_admin_xfer_ids ? max_admin_xfer_ids : "");
}
