ARG AI_PYTHON_IMAGE=python:3.11.15-slim-trixie
FROM ${AI_PYTHON_IMAGE}

WORKDIR /app
# OpenCV 的 manylinux wheel 使用 glibc；与现有图像 worker 一样采用 slim 运行时。
RUN groupadd --system edugrade && useradd --system --gid edugrade --uid 10001 --no-create-home edugrade

# 本地 OCR 只为模板类别提供第二类证据；中文/英文静态标签不再仅依赖多模态模型自报。
RUN apt-get update && apt-get install -y --no-install-recommends tesseract-ocr tesseract-ocr-eng tesseract-ocr-chi-sim \
    && rm -rf /var/lib/apt/lists/*

COPY ai-services/requirements.txt /app/requirements.txt
COPY packages/worker-runtime-python /tmp/worker-runtime-python
RUN python -m pip install --no-cache-dir -r /app/requirements.txt \
    && python -m pip install --no-cache-dir --no-deps /tmp/worker-runtime-python \
    && python -m pip uninstall -y pip

COPY ai-services/grading_agent /app/grading_agent
# 提示词与契约随镜像一起部署，运行时版本校验依赖这些固定资源。
COPY ai-services/prompts /app/prompts
COPY contracts/grading-agent /app/contracts/grading-agent

RUN mkdir -p /app/diagnostics/template-analysis && chown -R edugrade:edugrade /app
USER edugrade

ENV EDUGRADE_GRADING_AGENT_HOST=0.0.0.0 \
    EDUGRADE_GRADING_AGENT_PORT=8100 \
    EDUGRADE_GRADING_CONTRACT_ROOT=/app/contracts/grading-agent/v1 \
    EDUGRADE_GRADING_PROMPT_ROOT=/app/prompts \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
EXPOSE 8100
CMD ["python", "-m", "grading_agent"]
