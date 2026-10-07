# Can be deployed using E:\HTL\Infrastruktur\26-IT-Infrastruktur\docs\apps\filestash\filestashDeploy.ps1

# STEP1: SOURCE
# HTL: build from the LOCAL WORKING TREE by default - no clone, what you
# have on disk is what gets compiled (still: COMMIT + PUSH before shipping,
# the deploy script enforces it). Set USE_LOCAL=0 to build from a fresh
# git clone (upstream-style; GIT_REPO/GIT_BRANCH selectors below).
# USE_LOCAL is a build arg so switching modes cache-busts exactly one path.
ARG USE_LOCAL=1
ARG GIT_REPO=https://github.com/SteiningerSebastian/filestash
ARG GIT_BRANCH=master
FROM alpine/git AS builder_prepare
ARG USE_LOCAL
ARG GIT_REPO
ARG GIT_BRANCH
WORKDIR /home/
# HTL (CACHE ANTI-PATTERN KILLER): the build context is copied in TWO parts:
#   1. EVERYTHING EXCEPT dist/ -> feeds the Go build. A theme-zip rebuild
#      or any dist churn NEVER touches this layer, so `make init`/`make
#      build` stay cached (this was the ~10min rebuild trap).
#   2. dist/plugins/ -> copied at the VERY END (zip baking), one tiny layer.
COPY go.mod go.sum Makefile embed.go /home/localcheckout/
COPY cmd/ /home/localcheckout/cmd/
COPY server/ /home/localcheckout/server/
COPY public/ /home/localcheckout/public/
# HTL: the go:generate step (make init -> server/pkg/env/generator.go) runs
# `git rev-parse HEAD` to fill BUILD_REF; with a .git-less tree it silently
# writes an EMPTY string and ServeIndex then panics on `BUILD_REF[0:7]`
# (the 502 on every page). Ship a MINIMAL git metadata stub so rev-parse
# resolves: HEAD + refs/heads/master (sha file). No worktree checkout is
# needed - rev-parse HEAD only dereferences the ref.
COPY docker/git-stub/HEAD /home/localcheckout/.git/HEAD
COPY docker/git-stub/config /home/localcheckout/.git/config
COPY docker/git-stub/objects/marker /home/localcheckout/.git/objects/marker
COPY docker/git-stub/refs/heads/master /home/localcheckout/.git/refs/heads/master
RUN git -C /home/localcheckout rev-parse --short HEAD
RUN if [ "$USE_LOCAL" = "1" ]; then \
        echo "building from LOCAL working tree"; \
    else \
        rm -rf /home/localcheckout/* /home/localcheckout/.* 2>/dev/null; \
        git clone --depth 1 --single-branch --branch ${GIT_BRANCH} ${GIT_REPO} /tmp/remotecheckout && \
        cp -a /tmp/remotecheckout/. /home/localcheckout/ && rm -rf /tmp/remotecheckout && \
        git -C /home/localcheckout rev-parse --short HEAD; \
    fi

# STEP2: BUILD BACKEND
FROM golang:1.26-trixie AS builder_backend
# HTL: the fat apt installs live BEFORE the source COPY so source changes
# never re-run them - their layers are children of the base image, not of
# the changed COPY.
WORKDIR /home/filestash/
RUN apt-get update > /dev/null && \
    apt-get inyt-dev libswresample-dev libswscale-dev libavutil-dev > /dev/null 2>&1
# HTL: source COPY from the prepared stage. Docker keys this layer on the
# CONTENT of /home/localcheckout - local file changes (or a remote clone
# with USE_LOCAL=0) re-run exactly this COPY + the make layers below;
# the fat apt layers above stay cached.
COPY --from=builder_prepare /home/localcheckout/ .
# HTL: `make init` (go mod download) in its OWN layer with a cache mount, so
# a source change re-runs it but re-downloads ZERO go modules (module +
# build caches are persistent BuildKit cache mounts).
RUN --mount=type=cache,target=/go/pkg/mod \
    make init
# HTL: build layer depends on the fresh source -> re-runs on every source
# change while apt & module layers above stay cached.
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
