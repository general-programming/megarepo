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

void nvme_noreset_init(void)
{
	pr_info("nvme-noreset: patched nvme-core active (persist_err_noreset_ids=\"%s\" zero_discard_ids=\"%s\")\n",
		persist_err_noreset_ids ? persist_err_noreset_ids : "",
		zero_discard_ids ? zero_discard_ids : "");
}
