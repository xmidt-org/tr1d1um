## SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
## SPDX-License-Identifier: Apache-2.0
ARG ARCH
ARG PLATFORM

FROM docker.io/library/golang:1.23.2-alpine as builder
WORKDIR /src

RUN apk update && \
    apk add --no-cache --no-progress \
    ca-certificates \
    curl

# Download spruce here to eliminate the need for curl in the final image
RUN mkdir -p /go/bin
RUN curl -Lo /go/bin/spruce https://github.com/geofffranks/spruce/releases/download/v1.31.1/spruce-linux-$(go env GOARCH)
RUN chmod +x /go/bin/spruce
# Error out if spruce download fails by checking the version
RUN /go/bin/spruce --version

COPY . .

##########################
# Build the final image.
##########################

FROM --platform=${PLATFORM} alpine:latest

# Copy over the standard things you'd expect.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt  /etc/ssl/certs/
COPY tr1d1um /
COPY entrypoint.sh  /

# Copy over spruce and the spruce template file used to make the actual configuration file.
COPY tr1d1um_spruce.yaml  /tmp/tr1d1um_spruce.yaml
COPY --from=builder /go/bin/spruce        /bin/

# Include compliance details about the container and what it contains.
COPY Dockerfile /
COPY NOTICE     /
COPY LICENSE    /

# Make the location for the configuration file that will be used.
RUN     mkdir /etc/tr1d1um/ \
    &&  touch /etc/tr1d1um/tr1d1um.yaml \
    &&  chmod 666 /etc/tr1d1um/tr1d1um.yaml

USER nobody

ENTRYPOINT ["/entrypoint.sh"]

EXPOSE 6100
EXPOSE 6101
EXPOSE 6102
EXPOSE 6103

CMD ["/tr1d1um"]
