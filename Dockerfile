FROM golang:1.23-alpine AS builder

WORKDIR /app

# 第一步：先复制全部代码（让 go mod tidy 能扫描到所有依赖）
COPY . .

# 第二步：初始化模块 + 自动识别并下载所有依赖（包括第三方包和本地包）
RUN go mod init simple-astrm && \
    go mod tidy && \
    go mod download

# 第三步：静态编译
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