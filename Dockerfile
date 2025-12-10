FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod* go.sum* ./

RUN if [ ! -f go.mod ]; then go mod init simple-astrm; fi && \
    go get github.com/gin-gonic/gin@v1.9.1 && \
    go get github.com/sirupsen/logrus@v1.9.3 && \
    go get gopkg.in/yaml.v3@v3.0.1 && \
    go get github.com/andybalholm/brotli@v1.1.0 && \
    go mod tidy && go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-w -s" -o simple-astrm main.go

FROM alpine:3.20

RUN apk add --no-cache tzdata ca-certificates

ENV TZ=Asia/Shanghai

RUN adduser -D -H appuser
USER appuser

WORKDIR /app

COPY --from=builder /app/simple-astrm ./
COPY --from=builder /app/config.yaml.example ./config.yaml.example

EXPOSE 8095

CMD ["./simple-astrm"]