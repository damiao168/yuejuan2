ARG GO_BUILD_IMAGE=golang:1.26.6-alpine
ARG API_RUNTIME_IMAGE=alpine:3.22
ARG APP_VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_TIME=unknown
ARG IMAGE_DIGEST=unknown
ARG RELEASE_ID=local
ARG SCHEMA_VERSION=unknown

FROM ${GO_BUILD_IMAGE} AS builder

ARG APP_VERSION
ARG GIT_SHA
ARG BUILD_TIME
ARG IMAGE_DIGEST
ARG RELEASE_ID
ARG SCHEMA_VERSION
# Optional module proxy override for restricted-network builds; empty keeps
# the official default.
ARG GOPROXY=""

WORKDIR /src
COPY services/api-gateway/go.mod services/api-gateway/go.sum ./
RUN GOPROXY=${GOPROXY:-https://proxy.golang.org,direct} go mod download

COPY services/api-gateway/ ./
# 构建信息由发布流水线注入，运行时可用它核对镜像、代码和数据库版本是否匹配。
RUN GOPROXY=${GOPROXY:-https://proxy.golang.org,direct} CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-s -w \
      -X edugrade-enterprise/services/api-gateway/internal/buildinfo.Version=${APP_VERSION} \
      -X edugrade-enterprise/services/api-gateway/internal/buildinfo.GitSHA=${GIT_SHA} \
      -X edugrade-enterprise/services/api-gateway/internal/buildinfo.BuildTime=${BUILD_TIME} \
      -X edugrade-enterprise/services/api-gateway/internal/buildinfo.ImageDigest=${IMAGE_DIGEST} \
      -X edugrade-enterprise/services/api-gateway/internal/buildinfo.ReleaseID=${RELEASE_ID} \
      -X edugrade-enterprise/services/api-gateway/internal/buildinfo.SchemaVersion=${SCHEMA_VERSION}" \
    -o /out/api-gateway ./cmd/api-gateway
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o /out/file-reconcile ./tools/file-reconcile

FROM ${API_RUNTIME_IMAGE}

# 固定非 root 身份，挂载目录的权限需要允许该 UID 读写所需文件。
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S -g 10001 edugrade \
    && adduser -S -D -H -u 10001 -G edugrade edugrade
WORKDIR /app
COPY --from=builder /out/api-gateway /usr/local/bin/api-gateway
COPY --from=builder /out/file-reconcile /usr/local/bin/file-reconcile

USER edugrade

EXPOSE 8080
ENTRYPOINT ["api-gateway"]
