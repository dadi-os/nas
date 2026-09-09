# Headscale image is distroless (no shell). Stage its binary onto Debian so
# bootstrap.sh and the provision service can both exec `headscale`.
FROM headscale/headscale:0.26 AS headscale

FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /nas-service .

FROM debian:bookworm-slim AS production
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
      ca-certificates curl jq util-linux tmux ripgrep bash \
      xvfb chromium imagemagick fonts-liberation \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --system --create-home --uid 1000 --shell /usr/sbin/nologin dadi
ENV CHROMIUM_BIN=chromium
COPY --from=headscale /ko-app/headscale /usr/local/bin/headscale
COPY --from=build /nas-service /usr/local/bin/nas-service
EXPOSE 8080
CMD ["nas-service"]

FROM golang:1.23-bookworm AS dev
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
      ca-certificates curl jq util-linux docker.io tmux ripgrep bash \
      xvfb chromium imagemagick fonts-liberation \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --system --create-home --uid 1000 --shell /usr/sbin/nologin dadi \
  && go install github.com/air-verse/air@v1.61.7
ENV CHROMIUM_BIN=chromium
COPY --from=headscale /ko-app/headscale /usr/local/bin/headscale
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
COPY .air.toml ./
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]
