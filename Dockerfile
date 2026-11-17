FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -o /ingest ./cmd/ingest && \
    CGO_ENABLED=0 go build -trimpath -o /map-amounts ./cmd/map-amounts && \
    CGO_ENABLED=0 go build -trimpath -o /reconcile ./cmd/reconcile

FROM alpine:3.23
COPY --from=build /ingest /map-amounts /reconcile /usr/local/bin/
USER 65534:65534
ENTRYPOINT ["ingest", "-reports", "/reports"]
