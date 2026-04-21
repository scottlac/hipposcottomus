# ---- Build Stage ----
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static binary — no CGO
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /scraper .

# ---- Runtime Stage ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=builder /scraper /usr/local/bin/scraper

EXPOSE 8080

ENTRYPOINT ["scraper"]
