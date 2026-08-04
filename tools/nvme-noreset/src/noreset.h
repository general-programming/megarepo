/* SPDX-License-Identifier: GPL-2.0 */
/*
 * nvme-noreset: opt-in, PCI-ID-scoped workarounds for controllers whose
 * firmware asserts a persistent internal error in a loop.
 *
 * Everything here is inert unless a matching allow-list is passed as a
 * module parameter, so the default behaviour is identical to stock.
 */
#ifndef _NVME_NORESET_H
#define _NVME_NORESET_H

#include <linux/device.h>

void nvme_noreset_init(void);

/* True if @dev is on the persist_err_noreset_ids allow-list. */
bool nvme_noreset_suppress_reset(struct device *dev);

/* True if @dev is on the zero_discard_ids allow-list. */
bool nvme_noreset_zero_discard(struct device *dev);

/*
 * Admin-queue-only max_hw_sectors override, in 512B sectors, for @dev on the
 * max_admin_xfer_ids allow-list. 0 means "no override" (stock behaviour).
 */
u32 nvme_noreset_max_admin_sectors(struct device *dev);

#endif /* _NVME_NORESET_H */
