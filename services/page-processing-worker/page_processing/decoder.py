from __future__ import annotations

import io
import math
import warnings
from dataclasses import dataclass

import pypdfium2 as pdfium
from PIL import Image, ImageOps, ImageSequence, UnidentifiedImageError


class DecodeError(RuntimeError):
    pass


@dataclass(frozen=True)
class DecodedPage:
    index: int
    png: bytes
    width: int
    height: int


def decode_document(
    data: bytes,
    content_type: str,
    *,
    render_dpi: int = 300,
    max_pages: int = 500,
    max_page_pixels: int = 50_000_000,
    max_total_pixels: int = 1_000_000_000,
) -> tuple[list[DecodedPage], str]:
    if not data:
        raise DecodeError("empty_document")
    _validate_limits(render_dpi, max_pages, max_page_pixels, max_total_pixels)
    if data.startswith(b"%PDF-"):
        images = _decode_pdf(data, render_dpi, max_pages, max_page_pixels, max_total_pixels)
        detected = "application/pdf"
    else:
        images, detected = _decode_image(data, content_type, max_pages, max_page_pixels, max_total_pixels)
    pages: list[DecodedPage] = []
    total_pixels = 0
    try:
        for index, source_image in enumerate(images, start=1):
            image = ImageOps.exif_transpose(source_image).convert("RGB")
            try:
                pixels = image.width * image.height
                total_pixels += pixels
                if pixels <= 0 or pixels > max_page_pixels:
                    raise DecodeError("page_pixel_limit_exceeded")
                if total_pixels > max_total_pixels:
                    raise DecodeError("document_pixel_limit_exceeded")
                output = io.BytesIO()
                image.save(output, format="PNG", optimize=False)
                pages.append(DecodedPage(index=index, png=output.getvalue(), width=image.width, height=image.height))
            finally:
                image.close()
                source_image.close()
    finally:
        # Closing an already closed Pillow image is harmless and ensures pages
        # not reached after a limit/encoding failure do not retain buffers.
        for source_image in images:
            source_image.close()
    if not pages:
        raise DecodeError("document_has_no_pages")
    return pages, detected


def _validate_limits(render_dpi: int, max_pages: int, max_page_pixels: int, max_total_pixels: int) -> None:
    if not isinstance(render_dpi, int) or isinstance(render_dpi, bool) or not 36 <= render_dpi <= 600:
        raise DecodeError("render_dpi_out_of_range")
    if not isinstance(max_pages, int) or isinstance(max_pages, bool) or max_pages <= 0:
        raise DecodeError("page_limit_invalid")
    if not isinstance(max_page_pixels, int) or isinstance(max_page_pixels, bool) or max_page_pixels <= 0:
        raise DecodeError("page_pixel_limit_invalid")
    if not isinstance(max_total_pixels, int) or isinstance(max_total_pixels, bool) or max_total_pixels <= 0:
        raise DecodeError("document_pixel_limit_invalid")


def _decode_pdf(
    data: bytes,
    render_dpi: int,
    max_pages: int,
    max_page_pixels: int,
    max_total_pixels: int,
) -> list[Image.Image]:
    try:
        document = pdfium.PdfDocument(data)
    except Exception as exc:
        raise DecodeError("invalid_pdf") from exc
    try:
        if len(document) == 0 or len(document) > max_pages:
            raise DecodeError("pdf_page_limit_exceeded")
        scale = render_dpi / 72.0
        images: list[Image.Image] = []
        estimated_total_pixels = 0
        try:
            for page_index in range(len(document)):
                page = document[page_index]
                try:
                    width_points, height_points = page.get_size()
                    width = math.ceil(width_points * scale)
                    height = math.ceil(height_points * scale)
                    pixels = width * height
                    if pixels <= 0 or pixels > max_page_pixels:
                        raise DecodeError("page_pixel_limit_exceeded")
                    estimated_total_pixels += pixels
                    if estimated_total_pixels > max_total_pixels:
                        raise DecodeError("document_pixel_limit_exceeded")
                    # PDF 尺寸单位为 1/72 英寸；先按目标 DPI 估算像素，再分配渲染位图。
                    bitmap = page.render(scale=scale, rotation=0, rev_byteorder=True)
                    try:
                        images.append(bitmap.to_pil().copy())
                    finally:
                        bitmap.close()
                finally:
                    page.close()
            return images
        except DecodeError:
            for image in images:
                image.close()
            raise
        except Exception as exc:
            for image in images:
                image.close()
            raise DecodeError("invalid_pdf") from exc
    finally:
        document.close()


def _decode_image(
    data: bytes,
    content_type: str,
    max_pages: int,
    max_page_pixels: int,
    max_total_pixels: int,
) -> tuple[list[Image.Image], str]:
    tiff_expected = content_type in {"image/tiff", "image/tif"} or data.startswith((b"II*\x00", b"MM\x00*"))
    try:
        with warnings.catch_warnings():
            warnings.simplefilter("error", UserWarning)
            source = Image.open(io.BytesIO(data))
    except (UnidentifiedImageError, OSError, Image.DecompressionBombError, UserWarning) as exc:
        raise DecodeError("invalid_tiff" if tiff_expected else "unsupported_or_invalid_image") from exc
    try:
        frame_count = getattr(source, "n_frames", 1)
        if frame_count <= 0 or frame_count > max_pages:
            raise DecodeError("image_frame_limit_exceeded")
        is_tiff = (source.format or "").upper() == "TIFF"
        images: list[Image.Image] = []
        total_pixels = 0
        try:
            for frame in ImageSequence.Iterator(source):
                pixels = frame.width * frame.height
                total_pixels += pixels
                if pixels <= 0 or pixels > max_page_pixels:
                    raise DecodeError("page_pixel_limit_exceeded")
                if total_pixels > max_total_pixels:
                    raise DecodeError("document_pixel_limit_exceeded")
                images.append(frame.copy())
        except DecodeError:
            for image in images:
                image.close()
            raise
        except (OSError, ValueError, Image.DecompressionBombError) as exc:
            for image in images:
                image.close()
            raise DecodeError("invalid_tiff" if is_tiff or tiff_expected else "unsupported_or_invalid_image") from exc
        detected = "image/tiff" if is_tiff else Image.MIME.get(source.format or "", "image/unknown")
        return images, detected
    finally:
        source.close()
