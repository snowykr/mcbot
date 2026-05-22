FROM golang:1.25.9-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o mcbot ./cmd/mcbot


FROM alpine:3.19

RUN apk add --no-cache ca-certificates docker-cli tzdata

ENV TZ=Asia/Seoul

WORKDIR /app

COPY --from=builder /app/mcbot .

# Explicit runtime contract: mcbot bot run
ENTRYPOINT ["./mcbot"]
CMD ["bot", "run"]
