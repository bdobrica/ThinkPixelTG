# syntax=docker/dockerfile:1.19.0@sha256:b6afd42430b15f2d2a4c5a02b919e98a525b785b1aaff16747d2f623364e39b6
FROM golang:1.27.0-alpine3.23@sha256:3747dcba41c8b0db3211fda4db61638b980e17ac5bb3c94460a975a9cfe19395 AS build

ARG VERSION=dev
ARG VCS_REF=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${VCS_REF}" \
    -o /out/thinkpixeltg ./cmd/thinkpixeltg

FROM scratch
ARG VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE
LABEL org.opencontainers.image.title="ThinkPixelTG" \
      org.opencontainers.image.description="Governed tool gateway" \
      org.opencontainers.image.source="https://github.com/bdobrica/ThinkPixelTG" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="UNLICENSED"
COPY --from=build /out/thinkpixeltg /thinkpixeltg
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/thinkpixeltg"]
CMD ["--mode=production", "--http-address=:8080"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=2s --retries=3 \
    CMD ["/thinkpixeltg", "healthcheck", "http://127.0.0.1:8080/livez"]
