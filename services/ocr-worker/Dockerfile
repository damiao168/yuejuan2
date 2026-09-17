ARG OCR_PYTHON_IMAGE=python:3.11.15-slim-trixie
FROM ${OCR_PYTHON_IMAGE}

# A locally cached OCR image can be used as the base for fast rebuilds. Its
# final stage runs as nobody, so explicitly restore build-time privileges.
USER root

ARG INSTALL_PADDLEOCR=true
ARG INSTALL_PADDLEOCR_HPI=false
ARG PIP_INDEX_URL=https://pypi.org/simple
ARG DEBIAN_MIRROR=http://deb.debian.org/debian
ARG DEBIAN_SECURITY_MIRROR=http://deb.debian.org/debian-security

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

WORKDIR /app

RUN --mount=type=cache,target=/root/.cache/pip,sharing=locked \
    python -m pip install --index-url "$PIP_INDEX_URL" \
      "pip==26.1.2" "setuptools==83.0.0" \
    && if [ "$INSTALL_PADDLEOCR" = "true" ]; then \
      python -m pip install --index-url "$PIP_INDEX_URL" \
        "paddleocr==3.7.0" "paddlepaddle==3.3.1" "tokenizers==0.22.1" "Pillow>=10,<12" "matplotlib>=3.9,<4"; \
      if [ "$INSTALL_PADDLEOCR_HPI" = "true" ]; then \
        paddlex --install hpi-cpu; \
      fi; \
    fi

# FormulaRecognition imports ftfy lazily during LaTeX post-processing, and the
# guarded formula path uses MathText for render-back validation. Keep these in
# the always-installed layer so a dependency-complete base image can be reused
# with INSTALL_PADDLEOCR=false.
RUN --mount=type=cache,target=/root/.cache/pip,sharing=locked \
    python -m pip install --index-url "$PIP_INDEX_URL" "ftfy==6.3.1" "matplotlib>=3.9,<4"

# System libraries change far less often than application source. Keep this
# before the source COPY so editing the OCR worker does not invalidate apt's
# layer and download the same Debian packages again.
RUN sed -i \
      -e "s|http://deb.debian.org/debian-security|$DEBIAN_SECURITY_MIRROR|g" \
      -e "s|http://deb.debian.org/debian|$DEBIAN_MIRROR|g" \
      /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends libgomp1 libgl1 libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

# Copy frequently changed shared/runtime source only after the large Paddle and
# operating-system dependency layers. Normal worker code edits can now reuse
# those layers without downloading the same packages again.
COPY packages/worker-runtime-python /tmp/worker-runtime-python
COPY services/ocr-worker/pyproject.toml ./pyproject.toml
COPY services/ocr-worker/ocr_worker ./ocr_worker
# setuptools is already pinned above. Avoid creating two isolated build
# environments (and downloading setuptools twice) on every source rebuild.
RUN python -m pip install --no-cache-dir --no-deps --no-build-isolation /tmp/worker-runtime-python .

RUN install -d -o nobody -g nogroup \
      /home/ocr-worker/.paddlex \
      /home/ocr-worker/.modelscope \
      /home/ocr-worker/.cache \
      /home/ocr-worker/.cache/huggingface \
      /home/ocr-worker/.config \
      /home/ocr-worker/.config/matplotlib

# PaddleX initializes its cache while importing PaddleOCR. The runtime user is
# intentionally non-root, so give it a stable writable cache location. Keep
# these runtime-only variables after dependency installation so changing them
# does not invalidate the large Paddle image layer.
ENV HOME=/home/ocr-worker
ENV PADDLE_PDX_CACHE_HOME=/home/ocr-worker/.paddlex
ENV MPLCONFIGDIR=/home/ocr-worker/.config/matplotlib

USER nobody

# Generate the font index once in the image. Runtime render validation then
# starts without a permission warning or an avoidable per-container scan.
RUN python -c "from matplotlib import font_manager; font_manager._load_fontmanager()"

CMD ["python", "-m", "ocr_worker"]
