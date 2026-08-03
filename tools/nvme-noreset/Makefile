# SPDX-License-Identifier: GPL-2.0
KVER  ?= $(shell uname -r)
KDIR  ?= /lib/modules/$(KVER)/build
PWD   := $(CURDIR)

.PHONY: all modules clean check-crc

all: modules

modules:
	$(MAKE) -C $(KDIR) M=$(PWD)/src modules

clean:
	$(MAKE) -C $(KDIR) M=$(PWD)/src clean

# Verify the rebuilt nvme-core exports the same symbol CRCs as the stock one,
# i.e. stock nvme.ko / nvme-tcp.ko will still load against it.
check-crc:
	@./check-crc.sh "$(KVER)" "$(PWD)/src/Module.symvers"
