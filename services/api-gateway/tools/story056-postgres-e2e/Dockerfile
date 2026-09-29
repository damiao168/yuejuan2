# 镜像入口运行 internal/server 包的测试，未按 STORY-056 名称筛选。
FROM golang:1.26-alpine

WORKDIR /src
COPY services/api-gateway/go.mod services/api-gateway/go.sum ./
RUN go mod download
COPY services/api-gateway/ ./

CMD ["go", "test", "-count=1", "-v", "./internal/server"]
