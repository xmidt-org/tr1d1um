# Tr1d1um

[![Build Status](https://travis-ci.com/xmidt-org/tr1d1um.svg?branch=master)](https://travis-ci.com/xmidt-org/tr1d1um) 
[![codecov.io](http://codecov.io/github/xmidt-org/tr1d1um/coverage.svg?branch=master)](https://codecov.io/github/xmidt-org/tr1d1um?branch=master)
[![Go Report Card](https://goreportcard.com/badge/github.com/xmidt-org/tr1d1um)](https://goreportcard.com/report/github.com/xmidt-org/tr1d1um)
[![Apache V2 License](http://img.shields.io/badge/license-Apache%20V2-blue.svg)](https://github.com/xmidt-org/tr1d1um/blob/master/LICENSE)


## Summary
An implementation of the WebPA API which enables communication with TR-181 data model devices connected to the [XMiDT](https://github.com/xmidt-org/xmidt) cloud as well as subscription capabilities to device events.


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
* `make rpm`: builds an rpm containing Tr1d1um
* `make docker`: fetches dependencies and builds a docker image for Tr1d1um
* `make local-docker`: builds a docker image for Tr1d1um assuming dependencies
   have been fetched
* `make test`: runs unit tests with coverage for Tr1d1um
* `make clean`: deletes previously-built binaries and object files

### Docker

The docker image can be built either with the Makefile or by running a docker 
command.  Either option requires first getting the source code.

See [Makefile](#Makefile) on specifics of how to build the image that way.

For running a command, either you can run `docker build` after getting all 
dependencies, or make the command fetch the dependencies.  If you don't want to 
get the dependencies, run the following command:
```bash
docker build -t tr1d1um:local -f Dockerfile.local .
```
If you want to get the dependencies then build, run the following commands:
```bash
docker build -t tr1d1um:local -f Dockerfile .
```

For either command, if you want the tag to be a version instead of `local`, 
then replace `local` in the `docker build` command.

## Deploy

If you'd like to stand up `Tr1d1um` and the XMiDT cluster on Docker for local testing, refer to the [deploy README](https://github.com/xmidt-org/xmidt/tree/master/deploy/README.md).

You can also run the standalone `Tr1d1um` binary:
```bash
./tr1d1um
```

## Contributing

Refer to [CONTRIBUTING.md](CONTRIBUTING.md).

