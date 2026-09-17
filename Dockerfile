FROM golang:1.26-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o server .

FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata imagemagick libheif libde265

COPY --from=builder /app/server .
COPY migrations ./migrations

# HTTP API
EXPOSE 8080
# SMTP (plain)
EXPOSE 2525
# SMTPS (TLS)
EXPOSE 2465
# IMAP (plain)
EXPOSE 1143
# IMAPS (TLS)
EXPOSE 1993

CMD ["./server"]
