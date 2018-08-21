FROM golang:alpine as builder
MAINTAINER Jack Murdock <jack_murdock@comcast.com>

# build the binary
WORKDIR /go/src
RUN apk add --update --repository https://dl-3.alpinelinux.org/alpine/edge/testing/ git curl
RUN curl https://glide.sh/get | sh
COPY src/ /go/src/

RUN glide -q install --strip-vendor
RUN go build -o tr1d1um_linux_amd64 tr1d1um

EXPOSE 6100 6101 6102
RUN mkdir -p /etc/tr1d1um
VOLUME /etc/tr1d1um

# the actual image
FROM alpine:latest
RUN apk --no-cache add ca-certificates
RUN mkdir -p /etc/tr1d1um
VOLUME /etc/tr1d1um
WORKDIR /root/
COPY --from=builder /go/src/tr1d1um_linux_amd64 .
ENTRYPOINT ["./tr1d1um_linux_amd64"]
