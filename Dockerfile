# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/catalog-server ./cmd/catalog-server

FROM alpine:3
RUN adduser -D -H -u 10001 app \
 && mkdir -p /var/lib/spotlight && chown app:app /var/lib/spotlight
COPY --from=build /out/catalog-server /usr/local/bin/catalog-server
USER app
# Индекс строится во временном каталоге (os.TempDir) — /tmp должен быть доступен на запись.
# DATA_DIR — последний импорт каталога (POST /api/import), нужен том, чтобы пережить пересоздание.
ENV LISTEN_ADDR=:8080 \
    CATALOG_FILE=/data/catalog.xls \
    DATA_DIR=/var/lib/spotlight
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s \
  CMD wget -qO- http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["catalog-server"]
