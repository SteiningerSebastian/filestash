# STEP1: CLONE THE CODE
FROM alpine/git AS builder_prepare
WORKDIR /home/
# HTL: default to the FORK (OptsAfterLogin / OPTS UTF8 ON fix lives in the
# backend code built from here). Building without --build-arg used to clone
# UPSTREAM - silently shipping without the UTF-8 fix (verified: binary grep
# had 0 hits for 'OPTS UTF8 ON'/'OptsAfterLogin', mojibake persisted).
ARG GIT_REPO=https://github.com/SteiningerSebastian/filestash
ARG GIT_BRANCH=master
# HTL: cache-busting for the git clone ONLY. Docker keys every RUN layer on
# the build args it consumes, so passing a NEW GIT_REV invalidates exactly
# this clone step (fresh source -> the binary carries the latest commit)
# while the expensive apt-get layers in STAGE2 stay cached (their inputs
# don't reference GIT_REV). Usage:
#   docker build -t <tag> .                      # = GIT_REV unchanged -> cached clone
#   docker build --build-arg GIT_REV=<any-new-value> -t <tag> .   # fresh clone
# or resolve the real head first for a reproducible pin:
#   $rev = (git ls-remote https://github.com/SteiningerSebastian/filestash refs/heads/master).Split("`t")[0]
#   docker build --build-arg GIT_REV=$rev -t <tag> .
ARG GIT_REV=initial
RUN git clone --depth 1 --single-branch --branch ${GIT_BRANCH} ${GIT_REPO} filestash && \
    git -C filestash rev-parse --short HEAD

# STEP2: BUILD BACKEND
FROM golang:1.26-trixie AS builder_backend
# HTL: the fat apt installs live BEFORE the source COPY so re-cloning the
# repo (new GIT_REV) never re-runs them - their layers are children of the
# base image, not of the changed COPY.
WORKDIR /home/filestash/
RUN apt-get update > /dev/null && \
    apt-get install -y curl make > /dev/null 2>&1 && \
    apt-get install -y libjpeg-dev libtiff-dev libpng-dev libwebp-dev libraw-dev libheif-dev libgif-dev libvips-dev > /dev/null 2>&1 && \
    apt-get install -y libavcodec-dev libavdevice-dev libavfilter-dev libavformat-dev libswresample-dev libswscale-dev libavutil-dev > /dev/null 2>&1
# HTL: `make init` (go mod download) in its OWN layer with a cache mount, so
# a cache-busted rebuild (new GIT_REV -> fresh clone -> COPY layer changes)
# re-downloads ZERO go modules. Module + build cache are persistent mounts,
# they survive every rebuild (this is the same trick as a buildkit --no-cache
# on the source layers only).
RUN --mount=type=cache,target=/go/pkg/mod \
    make init
COPY --from=builder_prepare /home/filestash/filestash/ .
# HTL: build layer depends on the fresh source -> re-runs on every cache
# bust while apt layers & module downloads above stay cached.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    make build

# STEP3: BUILD PROD IMAGE
FROM debian:stable-slim
LABEL maintainer="mickael@kerjean.me"
WORKDIR /app/
COPY --from=builder_backend /home/filestash/dist/ .
RUN apt-get update > /dev/null && \
    apt-get -y upgrade > /dev/null && \
    apt-get install -y --no-install-recommends ca-certificates curl poppler-utils && \
    rm -rf /var/lib/apt/lists/*
RUN apt-get update > /dev/null && \
    apt-get install -y --no-install-recommends ffmpeg libbrotli1 && \
    rm -rf /var/lib/apt/lists/* && \
    rm -rf /tmp/*
# NOTE: trimming ffmpeg's GUI/GPU dep chain (libLLVM ~124MB + libz3 +
# mesa-libgallium, pulled via SDL→mesa, NOT linked by /app/filestash) was
# tried and REVERTED: purging cascades the ffmpeg binary + av libs away
# (Depends graph), and reinstalling the binary re-pulls libllvm19 again
# (lab-verified in a clean debian 2026-10-05). ffmpeg binary and the LLVM
# chain are inseparable in Debian — upstream parity stays; the fat layer
# (~200MB) is tolerated, push reliability is fixed at the firewall
# (Fortigate idle timeout on long upload PUTs) instead.
RUN useradd filestash && \
    mkdir -p /app/data/state/ && \
    chown -R filestash:filestash /app/ && \
    chmod 730 /app/filestash

# HTL: ship theme plugins INSIDE the image. The zips are INSTALLED AS ZIPS
# into Filestash's plugin dir — the loader (server/pkg/extension/discovery.
# go) scans state/plugins/*.zip and reads manifest.json + entrypoints
# directly from the zip (extracted loose files are IGNORED there).
# No ConfigMap/initContainer needed at deploy time. NOTE: /app/data is
# shadowed by the runtime PVC; a fresh PVC boots with the baked-in plugin,
# a previously-seeded PVC keeps its own copy (Filestash only READS it).
COPY dist/plugins/ /tmp/plugins/
RUN mkdir -p /app/data/state/plugins && \
    cp /tmp/plugins/*.zip /app/data/state/plugins/ && \
    chown -R filestash:filestash /app/data/state/plugins && \
    rm -rf /tmp/plugins

USER filestash
CMD ["/app/filestash"]
EXPOSE 8334
