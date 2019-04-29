DEFAULT: build

GO           ?= go
GOFMT        ?= $(GO)fmt
FIRST_GOPATH := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
APP          := tr1d1um
BINARY       := $(FIRST_GOPATH)/bin/$(APP)

PROGVER = $(shell grep 'applicationVersion.*= ' src/$(APP)/$(APP).go | awk '{print $$3}' | sed -e 's/\"//g')

.PHONY: glide-install
glide-install:
	cd src && glide install --strip-vendor

.PHONY: build
build: glide-install
	cd src/$(APP) && $(GO) build

rpm:
	mkdir -p ./OPATH/SOURCES
	tar -czvf ./OPATH/SOURCES/$(APP)-$(PROGVER).tar.gz . --exclude ./.git --exclude ./OPATH --exclude ./conf --exclude ./deploy --exclude ./vendor
	cp conf/$(APP).service ./OPATH/SOURCES/
	cp conf/$(APP).yaml  ./OPATH/SOURCES/
	rpmbuild --define "_topdir $(CURDIR)/OPATH" \
		--define "_version $(PROGVER)" \
		--define "_release 1" \
		-ba deploy/packaging/$(APP).spec

.PHONY: version
version:
	@echo $(PROGVER)


# If the first argument is "update-version"...
ifeq (update-version,$(firstword $(MAKECMDGOALS)))
  # use the rest as arguments for "update-version"
  RUN_ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
  # ...and turn them into do-nothing targets
  $(eval $(RUN_ARGS):;@:)
endif

.PHONY: update-version
update-version:
	@echo "Update Version $(PROGVER) to $(RUN_ARGS)"
	sed -i "s/$(PROGVER)/$(RUN_ARGS)/g" src/$(APP)/$(APP).go


.PHONY: install
install:
	echo go build -o $(BINARY) $(PROGVER)

.PHONY: release-artifacts
release-artifacts:
	GOOS=darwin GOARCH=amd64 go build -o ./OPATH/$(APP)-$(PROGVER).darwin-amd64
	GOOS=linux  GOARCH=amd64 go build -o ./OPATH/$(APP)-$(PROGVER).linux-amd64

.PHONY: docker
docker:
	docker build -f ./deploy/Dockerfile -t $(APP):$(PROGVER) .

# build docker without running modules
.PHONY: local-docker
local-docker:
	GOOS=linux  GOARCH=amd64 go build -o $(APP)_linux_amd64
	docker build -f ./deploy/Dockerfile.local -t $(APP):local .

.PHONY: style
style:
	! gofmt -d $$(find . -path ./vendor -prune -o -name '*.go' -print) | grep '^'

.PHONY: test
test:
	go test -o $(BINARY) -v -race  -coverprofile=cover.out $(go list ./... | grep -v "/vendor/")

.PHONY: test-cover
test-cover: test
	go tool cover -html=cover.out

.PHONY: codecov
codecov: test
	curl -s https://codecov.io/bash | bash

.PHONEY: it
it:
	./it.sh

.PHONY: clean
clean:
	rm -rf ./$(APP) ./OPATH ./coverage.txt ./vendor
