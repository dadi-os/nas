# Headscale image is distroless (no shell). Stage its binary onto Debian so
# bootstrap.sh and the provision service can both exec `headscale`.
FROM headscale/headscale:0.26 AS headscale

FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -o /nas-service .
RUN CGO_ENABLED=0 go build -o /dadi ./cmd/dadi

FROM golang:1.23-bookworm AS test
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
      ca-certificates util-linux procps tmux ripgrep bash \
      xvfb chromium imagemagick fonts-liberation \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --system --create-home --uid 1000 --shell /usr/sbin/nologin dadi
ENV CHROMIUM_BIN=chromium
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY cmd ./cmd
CMD ["go", "test", "./..."]

FROM debian:bookworm-slim AS production
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
      ca-certificates curl jq util-linux procps tmux ripgrep bash \
      xvfb chromium imagemagick fonts-liberation \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --system --create-home --uid 1000 --shell /usr/sbin/nologin dadi
ENV CHROMIUM_BIN=chromium
COPY --from=headscale /ko-app/headscale /usr/local/bin/headscale
COPY --from=build /nas-service /usr/local/bin/nas-service
COPY --from=build /dadi /usr/local/bin/dadi
EXPOSE 8080
CMD ["nas-service"]

FROM golang:1.23-bookworm AS dev
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
      ca-certificates curl jq util-linux docker.io procps tmux ripgrep bash \
      xvfb chromium imagemagick fonts-liberation \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --system --create-home --uid 1000 --shell /usr/sbin/nologin dadi \
  && go install github.com/air-verse/air@v1.61.7
ENV CHROMIUM_BIN=chromium
COPY --from=headscale /ko-app/headscale /usr/local/bin/headscale
WORKDIR /src
COPY go.mod go.sum ./
COPY *.go ./
COPY cmd ./cmd
COPY .air.toml ./
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]
