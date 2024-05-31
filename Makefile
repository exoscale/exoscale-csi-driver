PACKAGE := github.com/exoscale/exoscale-csi-driver
PROJECT_URL := https://$(PACKAGE)
GO_MAIN_PKG_PATH := ./cmd/exoscale-csi-driver

EXTRA_ARGS := -parallel 3 -count=1 -failfast

# Dependencies

# Requires: https://github.com/exoscale/go.mk
GO_MK_REF := v2.0.0

# make go.mk a dependency for all targets
.EXTRA_PREREQS = go.mk

ifndef MAKE_RESTARTS
# This section will be processed the first time that make reads this file.

# This causes make to re-read the Makefile and all included
# makefiles after go.mk has been cloned.
Makefile:
	@touch Makefile
endif

.PHONY: go.mk
.ONESHELL:
go.mk:
	@if [ ! -d "go.mk" ]; then
		git clone https://github.com/exoscale/go.mk.git
	fi
	@cd go.mk
	@if ! git show-ref --quiet --verify "refs/heads/${GO_MK_REF}"; then
		git fetch
	fi
	@if ! git show-ref --quiet --verify "refs/tags/${GO_MK_REF}"; then
		git fetch --tags
	fi
	git checkout --quiet ${GO_MK_REF}

go.mk/init.mk:
include go.mk/init.mk
go.mk/public.mk:
include go.mk/public.mk

## Targets

# Docker
go.mk/init.mk:
include Makefile.docker
