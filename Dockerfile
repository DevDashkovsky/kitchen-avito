# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kitchen ./cmd/kitchen
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/pressly/goose/v3/cmd/goose@v3.27.2

FROM alpine:3.23

COPY --from=builder /out/kitchen /kitchen
COPY --from=builder /out/goose /goose
COPY migrations /migrations

USER 65532:65532

ENTRYPOINT ["/kitchen"]
