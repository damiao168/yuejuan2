# 镜像入口运行 internal/server 包的测试，未按 STORY-056 名称筛选。
ARG GO_BUILD_IMAGE=golang:1.26.10-alpine
ARG NODE_RUNTIME_IMAGE=node:24-alpine

FROM ${GO_BUILD_IMAGE} AS go-toolchain

FROM ${NODE_RUNTIME_IMAGE} AS node-dependencies

WORKDIR /workspace
COPY package.json package-lock.json ./
COPY apps/desktop-client/package.json apps/desktop-client/package.json
COPY apps/student-portal/package.json apps/student-portal/package.json
COPY apps/web-admin/package.json apps/web-admin/package.json
COPY packages/design-tokens/package.json packages/design-tokens/package.json
COPY packages/sdk/package.json packages/sdk/package.json
COPY packages/shared-types/package.json packages/shared-types/package.json
COPY packages/ui/package.json packages/ui/package.json
RUN npm ci

FROM ${NODE_RUNTIME_IMAGE}

COPY --from=go-toolchain /usr/local/go /usr/local/go
COPY --from=go-toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV PATH=/usr/local/go/bin:${PATH} \
    GOTOOLCHAIN=local \
    HOME=/home/e2e \
    GOPATH=/home/e2e/go \
    GOMODCACHE=/home/e2e/go/pkg/mod \
    GOCACHE=/home/e2e/.cache/go-build \
    TMPDIR=/home/e2e/tmp

RUN addgroup -S -g 10001 e2e \
    && adduser -S -D -H -u 10001 -G e2e -h /home/e2e e2e

# 全包测试读取仓库级模板并运行真实 Node 客户端，目录层级必须保留。
WORKDIR /workspace/services/api-gateway
COPY services/api-gateway/go.mod services/api-gateway/go.sum ./
RUN mkdir -p "$GOMODCACHE" "$GOCACHE" "$TMPDIR" \
    && go mod download
COPY services/api-gateway/ ./
COPY infra/docker-compose/.env.example /workspace/infra/docker-compose/.env.example
COPY scripts/ci/exam-command-client.mjs /workspace/scripts/ci/exam-command-client.mjs
COPY tests/integration/exam-command-client.ts /workspace/tests/integration/exam-command-client.ts
COPY apps/web-admin/src/api/exams.ts apps/web-admin/src/api/client.ts apps/web-admin/src/api/query.ts apps/web-admin/src/api/userError.ts /workspace/apps/web-admin/src/api/
COPY apps/web-admin/src/features/exams/create/examCreateCommand.ts /workspace/apps/web-admin/src/features/exams/create/examCreateCommand.ts
COPY --from=node-dependencies /workspace/node_modules/esbuild /workspace/node_modules/esbuild
COPY --from=node-dependencies /workspace/node_modules/@esbuild /workspace/node_modules/@esbuild

# 测试身份只写 HOME 内的缓存和临时文件；源码及客户端依赖由 root 持有且可读。
RUN chown -R 0:0 /workspace \
    && chmod -R u=rwX,go=rX /workspace \
    && chown -R 10001:10001 /home/e2e

USER 10001:10001

CMD ["go", "test", "-count=1", "-v", "./internal/server"]
