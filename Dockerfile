FROM golang:1.26-alpine

WORKDIR /app

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN go build -o server .

EXPOSE 8080 2525 2465 1143 1993

CMD ["./server"]
