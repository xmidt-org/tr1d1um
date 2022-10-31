FROM docker.io/library/golang:1.19-alpine as builder

WORKDIR /src

ARG VERSION
ARG GITCOMMIT
ARG BUILDTIME

RUN apk add --no-cache --no-progress \
    ca-certificates \
    make \
    curl \
    git \
    openssh \
    gcc \
    libc-dev \
    upx

# Download spruce here to eliminate the need for curl in the final image
RUN mkdir -p /go/bin && \
    curl -L -o /go/bin/spruce https://github.com/geofffranks/spruce/releases/download/v1.29.0/spruce-linux-amd64 && \
    chmod +x /go/bin/spruce

COPY . .

RUN make test release

##########################
# Build the final image.
##########################

FROM alpine:latest

# Copy over the standard things you'd expect.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt  /etc/ssl/certs/
COPY --from=builder /src/tr1d1um                        /
COPY --from=builder /src/.release/docker/entrypoint.sh  /

# Copy over spruce and the spruce template file used to make the actual configuration file.
COPY --from=builder /src/.release/docker/tr1d1um_spruce.yaml  /tmp/tr1d1um_spruce.yaml
COPY --from=builder /go/bin/spruce                            /bin/

# Include compliance details about the container and what it contains.
COPY --from=builder /src/Dockerfile \
                    /src/NOTICE \
                    /src/LICENSE \
                    /src/CHANGELOG.md   /

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
