# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git gcc musl-dev sqlite-dev

COPY go.mod ./
RUN go mod download

COPY . .
RUN go mod tidy

RUN CGO_ENABLED=1 GOOS=linux go build -o al-royter ./cmd/server

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs tzdata

WORKDIR /app

COPY --from=builder /app/al-royter .
COPY --from=builder /app/config.yaml .
COPY --from=builder /app/web ./web

RUN mkdir -p /app/data

ENV PORT=8080
ENV HOST=0.0.0.0
ENV GIN_MODE=release

EXPOSE 8080

CMD ["./al-royter"]