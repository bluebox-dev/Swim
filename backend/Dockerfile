# build context: repo root
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg
COPY backend ./backend
# optional plugins: compiled into the server only when backend/cmd/server/plugins_gen.go
# (written by builder-infra/scripts/plugin.sh) imports them; agent/ is here because the
# plugins' agent halves are vetted and tested with everything else
COPY agent ./agent
COPY plugins ./plugins
RUN go vet ./... && go test ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/swim-server ./backend/cmd/server

# ntfs-unwof: libntfs-3g helper that rewrites WOF (CompactOS) files before capture
FROM debian:bookworm-slim AS unwof
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev ntfs-3g-dev \
    && rm -rf /var/lib/apt/lists/*
COPY backend/tools/ntfs-unwof/unwof.c /src/unwof.c
RUN mkdir -p /out && gcc -O2 -Wall -Werror -o /out/ntfs-unwof /src/unwof.c -lntfs-3g

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libvirt-clients qemu-utils wimtools ntfs-3g libwin-hivex-perl util-linux wixl msitools ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/swim-server /usr/local/bin/swim-server
COPY --from=unwof /out/ntfs-unwof /usr/local/bin/ntfs-unwof
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/swim-server"]
