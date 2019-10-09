FROM golang:alpine as builder
MAINTAINER Jack Murdock <jack_murdock@comcast.com>

WORKDIR /go/src/github.com/xmidt-org/tr1d1um
ARG VERSION=unknown
ARG GITCOMMIT=unknown
ARG BUILDTIME=unknown

RUN apk update && apk upgrade && \
    apk add --no-cache bash git openssh

COPY . .
RUN GO111MODULE=on go build \
  -o tr1d1um_linux_amd64 \
  -ldflags "-X 'main.BuildTime=${BUILDTIME}' -X main.GitCommit=${GITCOMMIT} -X main.Version=${VERSION}"

FROM alpine

RUN apk --no-cache add ca-certificates
RUN mkdir -p /etc/tr1d1um
VOLUME /etc/tr1d1um

EXPOSE 8080

COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/tr1d1um_linux_amd64 /
COPY tr1d1um.yaml /
COPY deploy/Dockerfile NOTICE LICENSE CHANGELOG.md /
ENTRYPOINT ["/tr1d1um_linux_amd64"]
