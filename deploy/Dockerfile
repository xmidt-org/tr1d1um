FROM docker.io/library/golang:1.13-alpine as builder

MAINTAINER Jack Murdock <jack_murdock@comcast.com>

WORKDIR /go/src/github.com/xmidt-org/tr1d1um

ARG VERSION=unknown
ARG GITCOMMIT=unknown
ARG BUILDTIME=unknown

ADD https://github.com/geofffranks/spruce/releases/download/v1.25.2/spruce-linux-amd64 /usr/local/bin/spruce
RUN chmod +x /usr/local/bin/spruce

RUN apk add --no-cache --no-progress \
    ca-certificates \
    make \
    git \
    openssh \
    gcc \
    libc-dev \
    upx

COPY . .
RUN make build

FROM alpine:latest

COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/tr1d1um.yaml /tr1d1um.yaml
COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/tr1d1um /tr1d1um
COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/deploy/Dockerfile /go/src/github.com/xmidt-org/tr1d1um/NOTICE /go/src/github.com/xmidt-org/tr1d1um/LICENSE /go/src/github.com/xmidt-org/tr1d1um/CHANGELOG.md /
COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/deploy/packaging/entrypoint.sh /entrypoint.sh
COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/deploy/packaging/tr1d1um_spruce.yaml /tmp/tr1d1um_spruce.yaml
COPY --from=builder /usr/local/bin/spruce /spruce

RUN mkdir /etc/tr1d1um/ && touch /etc/tr1d1um/tr1d1um.yaml && chmod 666 /etc/tr1d1um/tr1d1um.yaml

USER nobody

ENTRYPOINT ["/entrypoint.sh"]

EXPOSE 6100
EXPOSE 6101
EXPOSE 6102
EXPOSE 6103

CMD ["/tr1d1um"]