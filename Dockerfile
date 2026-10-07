# Build Stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 安装 ca-certificates
RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

# 编译纯静态二进制文件 (无需 CGO，支持跨架构构建)
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -ldflags="-w -s" -o reader ./cmd/server

# Final Stage (极小镜像)
FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

# 默认数据存储卷
VOLUME ["/data"]

ENV PORT=8080
ENV DATA_DIR=/data
ENV ADMIN_USER=admin
ENV ADMIN_PASSWORD=admin123
ENV TZ=Asia/Shanghai

COPY --from=builder /app/reader /app/reader

EXPOSE 8080

ENTRYPOINT ["/app/reader"]

