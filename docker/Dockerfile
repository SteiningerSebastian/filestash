# STEP1: CLONE THE CODE
FROM alpine/git as builder_prepare
WORKDIR /home/
ARG GIT_REPO=https://github.com/mickael-kerjean/filestash
ARG GIT_BRANCH=master
RUN git clone --depth 1 --single-branch --branch ${GIT_BRANCH} ${GIT_REPO}

# STEP2: BUILD BACKEND
FROM golang:1.26-trixie AS builder_backend
WORKDIR /home/filestash/
COPY --from=builder_prepare /home/filestash/ .
RUN apt-get update > /dev/null && \
    apt-get install -y curl make > /dev/null 2>&1 && \
    apt-get install -y libjpeg-dev libtiff-dev libpng-dev libwebp-dev libraw-dev libheif-dev libgif-dev libvips-dev > /dev/null 2>&1 && \
    apt-get install -y libavcodec-dev libavdevice-dev libavfilter-dev libavformat-dev libswresample-dev libswscale-dev libavutil-dev > /dev/null 2>&1 && \
    make init && \
    make build

# STEP3: BUILD PROD IMAGE
FROM debian:stable-slim
MAINTAINER mickael@kerjean.me
WORKDIR /app/
COPY --from=builder_backend /home/filestash/dist/ .

# HTL: ship frontend theme plugins INSIDE the image. Zip files placed in
# dist/plugins/ (build context) are extracted into Filestash's plugin dir
# (state/plugins - server/pkg/extension/discovery.go reads every *.zip
# there at boot). No ConfigMap/initContainer needed at deploy time.
# NOTE /app/data is shadowed by the runtime PVC; a previously-seeded PVC
# keeps its own copies and Filestash only READS the plugin dir.
COPY dist/plugins/ /tmp/plugins/
RUN mkdir -p /app/data/state/plugins && \
    for f in /tmp/plugins/*.zip; do \
        unzip -o -q "$f" -d /app/data/state/plugins/; \
    done && \
    rm -rf /tmp/plugins


RUN apt-get update > /dev/null && \
    apt-get -y upgrade > /dev/null && \
    apt-get install -y --no-install-recommends ca-certificates curl ffmpeg libbrotli1 poppler-utils unzip && \
    useradd filestash && \
    mkdir -p /app/data/state/ && \
    chown -R filestash:filestash /app/ && \
    chmod 730 /app/filestash && \
    rm -rf /var/lib/apt/lists/* && \
    rm -rf /tmp/*

USER filestash
CMD ["/app/filestash"]
EXPOSE 8334
