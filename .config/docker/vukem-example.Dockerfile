# syntax=docker/dockerfile:1

# A ready-made example app: `vukem new --example <todo|finance>` runs inside
# the build, so the image holds exactly what the CLI of this checkout
# scaffolds. Published to ghcr.io/alexgalhardo/vukem-example-todo and
# vukem-example-finance by release.yml; `vukem new --prebuilt` writes the
# compose.yaml that runs them. See docs/tooling.md#container-image.
# Build from the repository root:
#
#   docker build -f .config/docker/vukem-example.Dockerfile --build-arg EXAMPLE=todo -t vukem-example-todo .
#
# The app serves HTTP itself, so there is no proxy stage.
ARG GO_IMAGE=golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf
ARG BUN_IMAGE=oven/bun:1.4.5-alpine@sha256:da816670775f4c2b4e990b6b1847cf5540dcdc721a42033dd331ce7a88ba6a3b
ARG RUNTIME_IMAGE=alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

FROM ${GO_IMAGE} AS gen
ARG EXAMPLE=todo
WORKDIR /src
ENV CGO_ENABLED=0 GOWORK=off
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# --framework points the app at this checkout, so the image never depends on
# a tag that is not published yet. --no-install: the assets stage installs.
RUN go build -trimpath -o /usr/local/bin/vukem ./cmd/vukem \
	&& git config --global user.email build@localhost \
	&& git config --global user.name build \
	&& mkdir /gen && cd /gen && vukem new app --example "$EXAMPLE" --framework /src --no-install

FROM ${BUN_IMAGE} AS assets
WORKDIR /src
COPY --from=gen /gen/app/package.json ./
RUN bun install
COPY --from=gen /gen/app/ ./
RUN bun run build

FROM gen AS build
WORKDIR /gen/app
COPY --from=assets /src/resources/public/build resources/public/build
RUN tags="$(sed 's/#.*//' .buildtags 2>/dev/null | tr -s ', \t\r\n' ',' | sed 's/^,//; s/,$//')" \
	&& go build -trimpath -ldflags="-s -w" -tags "$tags" -o /out/app ./cmd/app

FROM ${RUNTIME_IMAGE}
ARG EXAMPLE=todo
ARG VERSION=devel
ARG REVISION=unknown

LABEL org.opencontainers.image.title="vukem-example-${EXAMPLE}" \
	org.opencontainers.image.description="Vukem ${EXAMPLE} example app, ready to run" \
	org.opencontainers.image.source="https://github.com/AlexGalhardo/vukem" \
	org.opencontainers.image.url="https://github.com/AlexGalhardo/vukem" \
	org.opencontainers.image.documentation="https://github.com/AlexGalhardo/vukem#readme" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.revision="${REVISION}"

RUN apk add --no-cache ca-certificates tzdata \
	&& adduser -D -H -u 10001 app \
	&& mkdir -p /app/storage \
	&& chown app:app /app/storage
WORKDIR /app
COPY --from=build /out/app /app/app
COPY --chmod=0755 <<'SH' /app/entrypoint.sh
#!/bin/sh
/app/app migrate && exec /app/app "$@"
SH
USER app
# A demo, not a deployment: the finance example needs APP_ENV=development to
# run without Stripe and a mail provider, which also lets its local seeders
# run. APP_KEY must be passed at runtime (`vukem new --prebuilt` writes one).
ARG APP_ENV=production
ENV APP_ENV=${APP_ENV} HOST=0.0.0.0 PORT=3000 LOG_FORMAT=json MAIL_MAILER=log
EXPOSE 3000
ENTRYPOINT ["/app/entrypoint.sh"]
