FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod tidy && go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-w -s" -o go-emby-proxy main.go

FROM alpine:3.20

RUN apk add --no-cache tzdata ca-certificates

ENV TZ=Asia/Shanghai

RUN adduser -D -H appuser
USER appuser

WORKDIR /app

COPY --from=builder /app/go-emby-proxy ./
COPY --from=builder /app/config.yaml.example ./config.yaml.example

EXPOSE 8095

CMD ["./simple-astrm"]