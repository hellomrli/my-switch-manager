# 在项目根目录构建：
#   docker build -f packaging/Dockerfile -t my-switch-manager .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /switchmgr ./cmd/switchmgr

FROM alpine:3.22
# ca-certificates：下载元数据库；tzdata：日志与撤销日志的时间戳按 TZ 显示
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /switchmgr /usr/local/bin/switchmgr
ENV SWITCHMGR_DATA=/data TZ=Asia/Shanghai
VOLUME /data
EXPOSE 8765
# 界面里「游戏库目录」填容器内路径，例如 /games
ENTRYPOINT ["switchmgr"]
CMD ["serve", "--listen", "0.0.0.0:8765"]
