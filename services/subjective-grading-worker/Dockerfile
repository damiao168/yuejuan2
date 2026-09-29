ARG SUBJECTIVE_PYTHON_IMAGE=python:3.11.15-slim-trixie
FROM ${SUBJECTIVE_PYTHON_IMAGE}

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
WORKDIR /app
COPY packages/worker-runtime-python /tmp/worker-runtime-python
COPY services/subjective-grading-worker/pyproject.toml ./pyproject.toml
COPY services/subjective-grading-worker/subjective_grading_worker ./subjective_grading_worker
RUN python -m pip install --no-cache-dir --timeout 180 --retries 10 "pip==26.1.2" "setuptools==83.0.0" \
    && python -m pip install --no-cache-dir --no-deps /tmp/worker-runtime-python .
USER nobody
# 健康检查读取最近成功轮询的标记，不能只凭 Python 进程存在判断任务通路正常。
HEALTHCHECK --interval=30s --timeout=5s --start-period=45s --retries=3 \
    CMD ["python", "-m", "subjective_grading_worker.healthcheck"]
CMD ["python", "-m", "subjective_grading_worker"]
