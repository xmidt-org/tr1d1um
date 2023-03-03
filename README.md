# tr1d1um

[![Build Status](https://github.com/xmidt-org/tr1d1um/actions/workflows/ci.yml/badge.svg)](https://github.com/xmidt-org/tr1d1um/actions/workflows/ci.yml)
[![codecov.io](http://codecov.io/github/xmidt-org/tr1d1um/coverage.svg?branch=main)](http://codecov.io/github/xmidt-org/tr1d1um?branch=main)
[![Go Report Card](https://goreportcard.com/badge/github.com/xmidt-org/tr1d1um)](https://goreportcard.com/report/github.com/xmidt-org/tr1d1um)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=xmidt-org_tr1d1um&metric=alert_status)](https://sonarcloud.io/dashboard?id=xmidt-org_tr1d1um)
[![Apache V2 License](http://img.shields.io/badge/license-Apache%20V2-blue.svg)](https://github.com/xmidt-org/tr1d1um/blob/main/LICENSE)
[![GitHub Release](https://img.shields.io/github/release/xmidt-org/tr1d1um.svg)](CHANGELOG.md)


## Summary
An implementation of the WebPA API which enables communication with TR-181 data model devices connected to the [XMiDT](https://github.com/xmidt-org/xmidt) cloud as well as subscription capabilities to device events.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Details](#details)
- [Build](#build)
- [Deploy](#deploy)
- [Contributing](#contributing)

## Code of Conduct

This project and everyone participating in it are governed by the [XMiDT Code Of Conduct](https://xmidt.io/code_of_conduct/). 
By participating, you agree to this Code.


## Details 
The WebPA API operations can be divided into the following categories:

### Device Statistics - `/stat` endpoint

Fetch the statistics (i.e. uptime) for a given device connected to the XMiDT cluster. This endpoint is a simple shadow of its counterpart on the `XMiDT` API. That is, `Tr1d1um` simply passes through the incoming request to `XMiDT` as it comes and returns whatever response `XMiDT` provided.

### CRUD operations - `/config` endpoints

Tr1d1um validates the incoming request, injects it into the payload of a SimpleRequestResponse [WRP](https://github.com/xmidt-org/wrp-c/wiki/Web-Routing-Protocol) message and sends it to XMiDT. It is worth mentioning that Tr1d1um encodes the outgoing `WRP` message in `msgpack` as it is the encoding XMiDT ultimately uses to communicate with devices.

### Event listener registration - `/hook(s)` endpoints
Devices connected to the XMiDT Cluster generate events (i.e. going offline). The webhooks library used by Tr1d1um leverages AWS SNS to publish these events. These endpoints then allow API users to both setup listeners of desired events and fetch the current list of configured listeners in the system.


## Build

### Source

In order to build from source, you need a working 1.x Go environment.
Find more information on the [Go website](https://golang.org/doc/install).

Then, clone the repository and build using make:

```bash
git clone git@github.com:xmidt-org/tr1d1um.git
cd tr1d1um
make build
```

### Makefile

The Makefile has the following options you may find helpful:
* `make build`: builds the Tr1d1um binary in the tr1d1um/src/tr1d1um folder
* `make docker`: fetches all dependencies from source and builds a Tr1d1um
   docker image
* `make local-docker`: vendors dependencies and builds a Tr1d1um docker image
   (recommended for local testing)
* `make test`: runs unit tests with coverage for Tr1d1um
* `make clean`: deletes previously-built binaries and object files

### RPM

First have a local clone of the source and go into the root directory of the 
repository.  Then use rpkg to build the rpm:
```bash
rpkg srpm --spec <repo location>/<spec file location in repo>
rpkg -C <repo location>/.config/rpkg.conf sources --outdir <repo location>'
```

### Docker

The docker image can be built either with the Makefile or by running a docker
command.  Either option requires first getting the source code.

See [Makefile](#Makefile) on specifics of how to build the image that way.

If you'd like to build it without make, follow these instructions based on your use case:

- Local testing
```bash
go mod vendor
docker build -t tr1d1um:local -f deploy/Dockerfile .
```
This allows you to test local changes to a dependency. For example, you can build 
a tr1d1um image with the changes to an upcoming changes to [webpa-common](https://github.com/xmidt-org/webpa-common) by using the [replace](https://golang.org/ref/mod#go) directive in your go.mod file like so:
```
replace github.com/xmidt-org/webpa-common v1.10.2 => ../webpa-common
```
**Note:** if you omit `go mod vendor`, your build will fail as the path `../webpa-common` does not exist on the builder container.

- Building a specific version
```bash
git checkout v0.5.1
docker build -t tr1d1um:v0.5.1 -f deploy/Dockerfile .
```

**Additional Info:** If you'd like to stand up a XMiDT docker-compose cluster, read [this](https://github.com/xmidt-org/xmidt/blob/master/deploy/docker-compose/README.md).

## Deploy

If you'd like to stand up `Tr1d1um` and the XMiDT cluster on Docker for local testing, refer to the [deploy README](https://github.com/xmidt-org/xmidt/tree/main/deploy/README.md).

You can also run the standalone `tr1d1um` binary with the default `tr1d1um.yaml` config file:
```bash
./tr1d1um
```

### Kubernetes

A helm chart can be used to deploy tr1d1um to kubernetes
```
helm install xmidt-tr1d1um deploy/helm/tr1d1um
```

## Contributing

Refer to [CONTRIBUTING.md](CONTRIBUTING.md).

