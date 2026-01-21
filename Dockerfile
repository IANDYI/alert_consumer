FROM golang:1.24-alpine as builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o alert-consumer ./cmd/alert-consumer/main.go

FROM alpine:latest

# Install netcat for healthchecks
RUN apk add --no-cache netcat-openbsd

WORKDIR /app

COPY --from=builder /app/alert-consumer .
RUN chmod 755 ./alert-consumer

EXPOSE 8081

CMD ["./alert-consumer"]
