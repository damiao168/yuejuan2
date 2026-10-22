ARG OCR_PYTHON_IMAGE=python:3.11.15-slim-trixie
FROM ${OCR_PYTHON_IMAGE}

# 本地缓存的 OCR 镜像可作为快速重建的基础镜像。它的
# 最终阶段使用 nobody 用户，因此需显式恢复构建阶段权限。
USER root

ARG INSTALL_PADDLEOCR=true
ARG INSTALL_PADDLEOCR_HPI=false
ARG PIP_INDEX_URL=https://pypi.org/simple
ARG DEBIAN_MIRROR=http://deb.debian.org/debian
ARG DEBIAN_SECURITY_MIRROR=http://deb.debian.org/debian-security

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app

# FormulaRecognition 在 LaTeX 后处理中延迟导入 ftfy，
# 受保护的公式路径使用 MathText 回渲染验证。这两项必须在
# Paddle 条件分支外安装，以支持 INSTALL_PADDLEOCR=false。
# 任意安装命令失败都终止这一层；不能让后续if或基础依赖安装掩盖Paddle/HPI失败。
RUN --mount=type=cache,target=/root/.cache/pip,sharing=locked \
    set -e; python -m pip install --index-url "$PIP_INDEX_URL" \
      "pip==26.1.2" "setuptools==83.0.0" \
    ; if [ "$INSTALL_PADDLEOCR" = "true" ]; then \
      python -m pip install --index-url "$PIP_INDEX_URL" \
        "paddleocr==3.7.0" "paddlepaddle==3.3.1" "tokenizers==0.22.1" "Pillow>=10,<12" "matplotlib>=3.9,<4"; \
      if [ "$INSTALL_PADDLEOCR_HPI" = "true" ]; then \
        paddlex --install hpi-cpu; \
      fi; \
    fi; \
    python -m pip install --index-url "$PIP_INDEX_URL" "ftfy==6.3.1" "matplotlib>=3.9,<4"

# 系统库的变化频率远低于应用源码。把此步骤
# 放在源码 COPY 前，避免修改 OCR 工作进程时使 apt 层缓存失效，
# 从而重复下载相同的 Debian 软件包。
RUN sed -i \
      -e "s|http://deb.debian.org/debian-security|$DEBIAN_SECURITY_MIRROR|g" \
      -e "s|http://deb.debian.org/debian|$DEBIAN_MIRROR|g" \
      /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends libgomp1 libgl1 libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

# 在大型 Paddle 和操作系统依赖层之后，
# 再复制经常变化的共享代码和运行时代码。常规工作进程代码修改
# 就能复用这些依赖层，无需再次下载相同的软件包。
COPY packages/worker-runtime-python /tmp/worker-runtime-python
COPY services/ocr-worker/pyproject.toml ./pyproject.toml
COPY services/ocr-worker/ocr_worker ./ocr_worker
# 上方已固定 setuptools 版本，避免每次源码重建时创建两个
# 隔离构建环境，并重复下载 setuptools。
RUN python -m pip install --no-cache-dir --no-deps --no-build-isolation /tmp/worker-runtime-python . \
    && install -d -o nobody -g nogroup \
      /home/ocr-worker/.paddlex \
      /home/ocr-worker/.modelscope \
      /home/ocr-worker/.cache \
      /home/ocr-worker/.cache/huggingface \
      /home/ocr-worker/.config \
      /home/ocr-worker/.config/matplotlib

# PaddleX 在导入 PaddleOCR 时初始化缓存。运行时用户
# 有意使用非 root 账号，因此要提供稳定且可写的缓存目录。
# 这些仅供运行时使用的变量放在依赖安装后，避免修改变量时
# 使大型 Paddle 镜像层缓存失效。
ENV HOME=/home/ocr-worker
ENV PADDLE_PDX_CACHE_HOME=/home/ocr-worker/.paddlex
ENV MPLCONFIGDIR=/home/ocr-worker/.config/matplotlib

USER nobody

# 在镜像中一次性生成字体索引。运行时渲染验证启动时
# 就不会出现权限警告，也无需每个容器重复扫描。
RUN python -c "from matplotlib import font_manager; font_manager._load_fontmanager()"

CMD ["python", "-m", "ocr_worker"]
