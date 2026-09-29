ARG NODE_BUILD_IMAGE=node:24-alpine
ARG WEB_RUNTIME_IMAGE=nginx:1.29-alpine

FROM ${NODE_BUILD_IMAGE} AS builder

WORKDIR /src
COPY package.json package-lock.json ./
COPY apps/web-admin/package.json apps/web-admin/package.json
COPY apps/desktop-client/package.json apps/desktop-client/package.json
COPY packages/shared-types/package.json packages/shared-types/package.json
COPY packages/design-tokens/package.json packages/design-tokens/package.json
COPY packages/sdk/package.json packages/sdk/package.json
COPY packages/ui/package.json packages/ui/package.json
RUN npm ci --workspace apps/web-admin --include-workspace-root

COPY packages/shared-types packages/shared-types
COPY packages/design-tokens packages/design-tokens
COPY packages/sdk packages/sdk
COPY packages/ui packages/ui
COPY apps/web-admin apps/web-admin
COPY tests/e2e/fixtures tests/e2e/fixtures
# Vite 参数在构建时写入静态资源；启动最终 Nginx 容器时再设环境变量不会改写页面。
ARG VITE_API_BASE_URL=
ARG VITE_RELEASE_ID=local
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
ENV VITE_RELEASE_ID=${VITE_RELEASE_ID}
RUN npm --workspace apps/web-admin run build

FROM ${WEB_RUNTIME_IMAGE}

COPY apps/web-admin/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /src/apps/web-admin/dist /usr/share/nginx/html

EXPOSE 80
