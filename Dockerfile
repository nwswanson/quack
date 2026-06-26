# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
	go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	mkdir -p /out \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/quack-server ./cmd/quack-server \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/quack-hardware-plugin ./cmd/quack-hardware-plugin

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --system --uid 10001 --create-home --home-dir /nonexistent --shell /usr/sbin/nologin quack \
	&& usermod -aG video quack \
	&& mkdir -p /var/lib/quack \
	&& chown -R quack:quack /var/lib/quack

COPY --from=build /out/quack-server /usr/local/bin/quack-server
COPY --from=build /out/quack-hardware-plugin /usr/local/bin/quack-hardware-plugin

USER quack

ENV ADMIN_ADDR=:8081
ENV PUBLIC_ADDR=:8080

EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/quack-server"]
CMD ["-root", "/var/lib/quack", "-database", "/var/lib/quack/quack.sqlite", "-hardware-plugin", "/usr/local/bin/quack-hardware-plugin"]
