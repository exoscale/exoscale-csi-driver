# This Dockerfile is intended for usage with GoReleaser, which expects that a prebuilt go binary is copied into the image.
# To avoid having to maintain and use two different Dockerfiles(one for local development and one for releasing) we follow the GoReleaser approach for local development as well.
# Please build your image using `make docker` instead of a direct docker command.

FROM alpine:3.21

RUN apk update
RUN apk add --no-cache \
    e2fsprogs \
    e2fsprogs-extra \
    xfsprogs \
    xfsprogs-extra \
    cryptsetup \
    ca-certificates \
    blkid \
    btrfs-progs
RUN update-ca-certificates

COPY exoscale-csi-driver /

ARG VERSION
ARG VCS_REF
ARG BUILD_DATE
LABEL org.label-schema.build-date=${BUILD_DATE} \
      org.label-schema.vcs-ref=${VCS_REF} \
      org.label-schema.vcs-url="https://github.com/exoscale/exoscale-csi-driver" \
      org.label-schema.version=${VERSION} \
      org.label-schema.name="exoscale-csi-driver" \
      org.label-schema.vendor="Exoscale" \
      org.label-schema.description="Exoscale Scalable Blockstorage Container Storage Interface Driver" \
      org.label-schema.schema-version="1.0"

ENTRYPOINT ["/exoscale-csi-driver"]
