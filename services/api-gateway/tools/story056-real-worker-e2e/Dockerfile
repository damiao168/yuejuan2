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

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 e2e \
    && adduser -S -D -H -u 10001 -G e2e -h /home/e2e e2e \
    && mkdir -p /home/e2e \
    && chown 10001:10001 /home/e2e
ENV HOME=/home/e2e
COPY --from=builder /out/story056-real-worker-e2e /usr/local/bin/story056-real-worker-e2e
# 客户端结果写 stdout，由宿主 runner 收集；只有 HOME 和 /tmp 需要可写。
USER 10001:10001
ENTRYPOINT ["story056-real-worker-e2e"]
