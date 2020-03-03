FROM docker.io/library/golang:1.14-alpine as builder

MAINTAINER Jack Murdock <jack_murdock@comcast.com>

WORKDIR /go/src/github.com/xmidt-org/tr1d1um

ARG VERSION=unknown
ARG GITCOMMIT=unknown
ARG BUILDTIME=unknown


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

FROM scratch

COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/tr1d1um.yaml /tr1d1um.yaml
COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/tr1d1um /tr1d1um
COPY --from=builder /go/src/github.com/xmidt-org/tr1d1um/deploy/Dockerfile /go/src/github.com/xmidt-org/tr1d1um/NOTICE /go/src/github.com/xmidt-org/tr1d1um/LICENSE /go/src/github.com/xmidt-org/tr1d1um/CHANGELOG.md /

EXPOSE 6100
EXPOSE 6101
EXPOSE 6102
EXPOSE 6103

USER nobody

ENTRYPOINT ["/tr1d1um"]
