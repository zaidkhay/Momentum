# See ARCHITECTURE.md §3.2 — Go ingestion service
# Multi-stage build: compile then copy binary into minimal image

FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o ingestion .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/ingestion .
CMD ["./ingestion"]
