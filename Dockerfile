# syntax=docker/dockerfile:1

# Dockerfile per OctoTracker (Go) - immagine minima distroless
#
# Stage 1: compila un binario statico (CGO disabilitato, SQLite puro Go).
# Il build gira sull'architettura dell'host e fa cross-compile verso il
# target (linux/amd64, linux/arm64): niente emulazione QEMU per la compilazione.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build

WORKDIR /src

# Dipendenze prima del codice, per sfruttare la cache dei layer
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY webapp ./webapp

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/octotracker ./cmd/octotracker

# Stage 2: runtime. distroless/static contiene solo certificati CA, tzdata
# e /etc/passwd: nessuna shell, nessun package manager.
FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=build /out/octotracker /app/octotracker

# Variabili d'ambiente di default (possono essere sovrascritte)
ENV TELEGRAM_BOT_TOKEN=""
ENV SCRAPER_HOUR="11"
ENV CHECKER_HOUR="12"
ENV TZ="Europe/Rome"
ENV LOG_LEVEL="INFO"

EXPOSE 8443 8444

# L'immagine non ha shell: l'health check usa il sottocomando del binario
HEALTHCHECK --interval=60s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/app/octotracker", "healthcheck"]

# I dati (database e log) stanno in /app/data, montata come volume
ENTRYPOINT ["/app/octotracker"]
CMD ["serve"]
