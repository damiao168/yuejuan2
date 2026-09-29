ARG QUALITY_PYTHON_IMAGE=python:3.11.15-slim-trixie
FROM ${QUALITY_PYTHON_IMAGE}

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app

COPY packages/worker-runtime-python /tmp/worker-runtime-python
COPY services/image-quality-worker/pyproject.toml ./pyproject.toml
RUN python -m pip install --no-cache-dir --timeout 180 --retries 10 \
      "pip==26.1.2" "setuptools==83.0.0" \
    && python -m pip install --no-cache-dir --timeout 180 --retries 10 \
      "numpy==2.4.6" "opencv-python-headless==5.0.0.93" "pillow==12.3.0"

COPY services/image-quality-worker/image_quality ./image_quality
# 图像依赖已在上一层固定安装；此处只装共享运行时与业务包，保持镜像依赖可追溯。
RUN python -m pip install --no-cache-dir --no-deps /tmp/worker-runtime-python .

USER nobody

CMD ["python", "-m", "image_quality"]
