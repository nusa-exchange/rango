FROM golang:1.18.1-alpine AS builder

RUN apk add --no-cache curl git
RUN curl -Lo /usr/bin/kaigara https://github.com/nusa-exchange/kaigara/releases/download/0.0.1/kaigara \
  && chmod +x /usr/bin/kaigara

WORKDIR /build
ENV CGO_ENABLED=1 \
  GOOS=linux \
  GOARCH=amd64

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build ./cmd/rango


FROM alpine:3.9

RUN apk add ca-certificates
WORKDIR app
COPY --from=builder /build/rango ./
COPY --from=builder /usr/bin/kaigara /usr/bin/kaigara
RUN mkdir -p /app/config

CMD ["./rango"]
