# Headscale image is distroless (no shell). Stage its binary onto Debian so
# bootstrap.sh and the provision service can both exec `headscale`.
FROM headscale/headscale:0.26 AS headscale

FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -o /nas-service .

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates jq \
  && rm -rf /var/lib/apt/lists/*
COPY --from=headscale /ko-app/headscale /usr/local/bin/headscale
COPY --from=build /nas-service /usr/local/bin/nas-service
EXPOSE 8080
CMD ["nas-service"]
