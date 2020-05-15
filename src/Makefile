DATE_VERSION := $(shell date +%Y%m%d-%H%M)
GIT_VERSION := $(shell git rev-parse --short HEAD)
GIT_DATE_VERSION := $(GIT_VERSION)-$(DATE_VERSION)

release:
	go build --ldflags "-s -w -X main.appVer=$(GIT_DATE_VERSION)" .