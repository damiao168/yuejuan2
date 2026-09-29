ARG AI_PYTHON_IMAGE=python:3.12.13-alpine3.24
FROM ${AI_PYTHON_IMAGE}

WORKDIR /app
RUN addgroup -S edugrade && adduser -S -G edugrade -u 10001 edugrade

COPY ai-services/requirements.txt /app/requirements.txt
COPY packages/worker-runtime-python /tmp/worker-runtime-python
RUN python -m pip install --no-cache-dir -r /app/requirements.txt \
    && python -m pip install --no-cache-dir --no-deps /tmp/worker-runtime-python \
    && python -m pip uninstall -y pip

COPY ai-services/grading_agent /app/grading_agent
# 提示词与契约随镜像一起部署，运行时版本校验依赖这些固定资源。
COPY ai-services/prompts /app/prompts
COPY contracts/grading-agent /app/contracts/grading-agent

RUN chown -R edugrade:edugrade /app
USER edugrade

ENV EDUGRADE_GRADING_AGENT_HOST=0.0.0.0 \
    EDUGRADE_GRADING_AGENT_PORT=8100 \
    EDUGRADE_GRADING_CONTRACT_ROOT=/app/contracts/grading-agent/v1 \
    EDUGRADE_GRADING_PROMPT_ROOT=/app/prompts \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
EXPOSE 8100
CMD ["python", "-m", "grading_agent"]
