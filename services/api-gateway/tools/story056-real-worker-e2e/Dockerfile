# 构建阶段编译独立 E2E 客户端，运行阶段只保留证书和单个可执行文件。
ARG GO_BUILD_IMAGE=golang:1.26-alpine
ARG API_RUNTIME_IMAGE=alpine:3.22

FROM ${GO_BUILD_IMAGE} AS builder

WORKDIR /src
COPY services/api-gateway/go.mod services/api-gateway/go.sum ./
RUN go mod download

COPY services/api-gateway/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/story056-real-worker-e2e ./tools/story056-real-worker-e2e

FROM ${API_RUNTIME_IMAGE}

RUN apk add --no-cache ca-certificates
COPY --from=builder /out/story056-real-worker-e2e /usr/local/bin/story056-real-worker-e2e
ENTRYPOINT ["story056-real-worker-e2e"]
