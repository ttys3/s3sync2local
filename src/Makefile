DATE_VERSION := $(shell date +%Y%m%d-%H%M)
GIT_VERSION := $(shell git rev-parse --short HEAD)
GIT_DATE_VERSION := $(GIT_VERSION)-$(DATE_VERSION)
TARGET_EXEC := s3sync2local

debug:
	go build --ldflags "-X main.appVer=$(GIT_DATE_VERSION)" .

release:
	GOOS=linux go build -o $(TARGET_EXEC) -ldflags "-s -w -X main.appVer=$(GIT_DATE_VERSION)" .
	GOOS=windows go build -o $(TARGET_EXEC).exe -ldflags "-s -w -X main.appVer=$(GIT_DATE_VERSION)" .
	GOOS=darwin go build -o $(TARGET_EXEC).darwin -ldflags "-s -w -X main.appVer=$(GIT_DATE_VERSION)" .
	ls -lhp --color $(TARGET_EXEC)*

clean:
	rm -f $(TARGET_EXEC)*