# Foghorn server in a container.
#
#   docker compose up -d          (from the repository root; see docker-compose.yml)
#
# or by hand, from this folder:
#
#   docker build -t foghorn-server .
#   docker run -d --name foghorn -p 8080:8080 -v foghorn-data:/data foghorn-server
#
# Everything the server keeps (accounts, alerts, config.json, the log and
# FIRST-RUN.txt) is in /data, so keep that on a volume. See
# docs/INSTALL-SERVER.md, Appendix C.

# ---- build ----
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY . .
# -mod=vendor: the one dependency (golang.org/x/sys, Windows only) is vendored,
# and go.mod's replace points at a folder that only exists on the dev machine.
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags "-s -w" -o /out/foghorn-server . \
 && /out/foghorn-server version

# ---- run ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 foghorn \
 && adduser -S -D -H -u 10001 -G foghorn foghorn \
 && install -d -o foghorn -g foghorn -m 0750 /data
COPY --from=build /out/foghorn-server /usr/local/bin/foghorn-server

# FOGHORN_IN_CONTAINER turns off "Update server" in the console: the program is
# part of the image, so you update by pulling or rebuilding the image instead.
ENV FOGHORN_DATA=/data \
    FOGHORN_IN_CONTAINER=1
USER foghorn
VOLUME /data
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["foghorn-server"]
CMD ["run"]
