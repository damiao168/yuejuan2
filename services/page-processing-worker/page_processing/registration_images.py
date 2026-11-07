"""配准所用图像/PDF 解码；逐页释放渲染资源并限制模板像素预算。"""

from __future__ import annotations

import math

import cv2
import numpy as np
import pypdfium2 as pdfium

from .decoder import DecodeError, decode_document
from .registration_errors import RegistrationError


def _decode_first(data: bytes, content_type: str, render_dpi: int) -> np.ndarray:
    return _decode_page(data, content_type, render_dpi, 1)


def _decode_page(data: bytes, content_type: str, render_dpi: int, page_index: int) -> np.ndarray:
    if page_index < 1 or page_index > 100:
        raise RegistrationError("invalid_template_page")
    if data.startswith(b"%PDF-"):
        return _decode_pdf_page(data, render_dpi, page_index)
    try:
        pages, _ = decode_document(data, content_type, render_dpi=render_dpi, max_pages=100, max_page_pixels=60_000_000, max_total_pixels=600_000_000)
    except DecodeError as exc:
        raise RegistrationError(str(exc)) from exc
    if page_index > len(pages):
        raise RegistrationError("template_page_missing")
    return _decode_png(pages[page_index - 1].png)


def _decode_pdf_page(data: bytes, render_dpi: int, page_index: int) -> np.ndarray:
    # 扫描图生成的 PDF 可能把像素数写成物理点数；固定 300 DPI 会把模板重复放大。
    # 配准只需要目标页的版面比例，按目标 DPI 与像素预算中较小者渲染，题区仍按归一化坐标映射。
    if not isinstance(render_dpi, int) or isinstance(render_dpi, bool) or not 36 <= render_dpi <= 600:
        raise RegistrationError("render_dpi_out_of_range")
    try:
        document = pdfium.PdfDocument(data)
        try:
            if len(document) > 100:
                raise RegistrationError("pdf_page_limit_exceeded")
            if page_index > len(document):
                raise RegistrationError("template_page_missing")
            page = document[page_index - 1]
            try:
                width, height = page.get_size()
                if not all(math.isfinite(value) and value > 0 for value in (width, height)):
                    raise RegistrationError("invalid_pdf_page_size")
                scale = min(render_dpi / 72.0, 4096 / max(width, height), math.sqrt(16_000_000 / (width * height)))
                bitmap = page.render(scale=scale)
                try:
                    image = bitmap.to_pil()
                    try:
                        rgb = image.convert("RGB")
                        try:
                            return np.array(rgb)
                        finally:
                            rgb.close()
                    finally:
                        image.close()
                finally:
                    bitmap.close()
            finally:
                page.close()
        finally:
            document.close()
    except RegistrationError:
        raise
    except Exception as exc:
        raise RegistrationError("invalid_pdf") from exc


def _decode_png(data: bytes) -> np.ndarray:
    encoded = np.frombuffer(data, np.uint8)
    image = cv2.imdecode(encoded, cv2.IMREAD_COLOR)
    if image is None:
        raise RegistrationError("image_decode_failed")
    return cv2.cvtColor(image, cv2.COLOR_BGR2RGB)


def _encode_png(image: np.ndarray) -> bytes:
    bgr = cv2.cvtColor(image, cv2.COLOR_RGB2BGR)
    ok, encoded = cv2.imencode(".png", bgr, [cv2.IMWRITE_PNG_COMPRESSION, 6])
    if not ok:
        raise RegistrationError("image_encode_failed")
    return encoded.tobytes()
