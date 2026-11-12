# syntax=docker/dockerfile:1

# The Vukem toolchain image: the vukem CLI plus everything it shells out to
# (Go, Bun, git). Published to ghcr.io/alexgalhardo/vukem by release.yml; see
# docs/tooling.md#container-image. Build from the repository root:
#
#   docker build -f .config/docker/vukem.Dockerfile -t vukem .
#
# Debian (glibc), not Alpine: Bun's default Linux build links glibc, and the
# golang Debian image already ships git and ca-certificates.
ARG GO_IMAGE=golang:1.27.4@sha256:5e8afd493a7124a8586e134d9b532731161d4b9becf9dee5a5956b107426f93d

FROM ${GO_IMAGE} AS build
ARG VERSION=devel
WORKDIR /src
ENV CGO_ENABLED=0 GOWORK=off
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The .git directory travels in the context so the tag at HEAD becomes the
# version `vukem version` prints (Go stamps it from VCS). Without .git (a plain
# download) the binary reports "devel"; a release passes VERSION, and the build
# fails if the stamped version does not match. Build from a clone, not from a
# git worktree: its .git file points outside the context.
RUN git config --global --add safe.directory /src \
	&& go build -trimpath -ldflags="-s -w" -o /out/vukem ./cmd/vukem \
	&& if [ "$VERSION" != devel ]; then [ "$(/out/vukem version)" = "vukem v$VERSION" ]; fi

FROM ${GO_IMAGE}
ARG BUN_VERSION=1.4.4
ARG BUN_SHA256_AMD64=bf8ccbfb2b0b80159e0f9a6d15c55317add8077534dbc6130a3edb52fccb395c
ARG BUN_SHA256_ARM64=baa6bb36f51d6e58561e02bbea825038a9df454f673d6d776e8bbf86f8c7850e
ARG TARGETARCH
ARG VERSION=devel
ARG REVISION=unknown

LABEL org.opencontainers.image.title="vukem" \
	org.opencontainers.image.description="Vukem CLI with the Go and Bun toolchain" \
	org.opencontainers.image.source="https://github.com/AlexGalhardo/vukem" \
	org.opencontainers.image.url="https://github.com/AlexGalhardo/vukem" \
	org.opencontainers.image.documentation="https://github.com/AlexGalhardo/vukem#readme" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.revision="${REVISION}"

RUN case "$TARGETARCH" in \
		amd64) arch=x64; sum="$BUN_SHA256_AMD64" ;; \
		arm64) arch=aarch64; sum="$BUN_SHA256_ARM64" ;; \
		*) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
	esac \
	&& apt-get update \
	&& apt-get install -y --no-install-recommends unzip \
	&& rm -rf /var/lib/apt/lists/* \
	&& curl -fsSL -o /tmp/bun.zip \
		"https://github.com/oven-sh/bun/releases/download/bun-v${BUN_VERSION}/bun-linux-${arch}.zip" \
	&& echo "${sum}  /tmp/bun.zip" | sha256sum -c - \
	&& unzip -q /tmp/bun.zip -d /tmp \
	&& install -m 0755 "/tmp/bun-linux-${arch}/bun" /usr/local/bin/bun \
	&& ln -s bun /usr/local/bin/bunx \
	&& rm -rf /tmp/bun.zip "/tmp/bun-linux-${arch}" \
	&& useradd --uid 1000 --create-home --shell /bin/bash vukem \
	&& mkdir -p /work \
	&& chown vukem:vukem /work \
	&& git config --system --add safe.directory /work

COPY --from=build /out/vukem /usr/local/bin/vukem

# HOST=0.0.0.0 makes `vukem serve` reachable through `-p 3000:3000`; the
# framework default, localhost, would only listen inside the container.
ENV HOME=/home/vukem \
	GOPATH=/home/vukem/go \
	GOCACHE=/home/vukem/.cache/go-build \
	BUN_INSTALL_CACHE_DIR=/home/vukem/.bun/install/cache \
	PATH=/home/vukem/go/bin:/usr/local/go/bin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
	HOST=0.0.0.0 PORT=3000
USER vukem
WORKDIR /work
EXPOSE 3000
ENTRYPOINT ["vukem"]
