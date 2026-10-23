ARG PAGE_PROCESSING_PYTHON_IMAGE=python:3.11.15-slim-trixie
FROM ${PAGE_PROCESSING_PYTHON_IMAGE}

# 自定义运行时基础镜像可能默认使用非特权用户。软件包安装
# 必须使用 root；最终进程再降权为 nobody。
USER root

# 图像处理依赖的 wheel 下载量较大。保留可见的失败信息，
# 同时容忍 CI 和开发 Docker 环境中
# 软件包索引偶发的读取缓慢。
ARG PIP_RETRIES=5
ARG PIP_TIMEOUT=120

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app

COPY packages/worker-runtime-python /tmp/worker-runtime-python
COPY services/page-processing-worker/pyproject.toml ./pyproject.toml
COPY services/page-processing-worker/page_processing ./page_processing
RUN python -m pip install --no-cache-dir --retries "${PIP_RETRIES}" --timeout "${PIP_TIMEOUT}" \
      "pip==26.2.2" "setuptools==83.0.0" \
    && python -m pip install --no-cache-dir --retries "${PIP_RETRIES}" --timeout "${PIP_TIMEOUT}" \
      /tmp/worker-runtime-python .

USER nobody
CMD ["python", "-m", "page_processing"]
