ARG MATH_PYTHON_IMAGE=python:3.11.15-slim-trixie
FROM ${MATH_PYTHON_IMAGE}
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
WORKDIR /app
COPY services/math-verification-worker/pyproject.toml ./pyproject.toml
COPY services/math-verification-worker/math_verification_worker ./math_verification_worker
RUN python -m pip install --no-cache-dir --timeout 180 --retries 10 "pip==26.1.2" "setuptools==83.0.0" \
    && python -m pip install --no-cache-dir .
# 容器仅处理内部数学请求；以非特权用户运行，健康探针地址可随 TLS 部署调整。
USER nobody
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["python", "-c", "import os, urllib.request; url = os.getenv('EDUGRADE_MATH_VERIFY_HEALTH_URL', 'http://127.0.0.1:8092/healthz'); urllib.request.urlopen(url, timeout=3)"]
CMD ["python", "-m", "math_verification_worker"]
