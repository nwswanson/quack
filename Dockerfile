# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/quack-server ./cmd/quack-server

FROM debian:bookworm-slim AS runtime

RUN useradd --system --uid 10001 --create-home --home-dir /nonexistent --shell /usr/sbin/nologin quack \
	&& mkdir -p /var/lib/quack \
	&& chown -R quack:quack /var/lib/quack

COPY --from=build /out/quack-server /usr/local/bin/quack-server

USER quack

ENV ADDR=:8080
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/quack-server"]
CMD ["-root", "/var/lib/quack", "-database", "/var/lib/quack/quack.sqlite", "-admin-host", "https://quack.k3s.nathanielswanson.com"]
