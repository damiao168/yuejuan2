from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from page_processing import decoder
from PIL import Image


def source_frames(monkeypatch: pytest.MonkeyPatch, *, source_format: str = "TIFF", copy_error: type[Exception] | None = None) -> tuple[SimpleNamespace, list[SimpleNamespace], list[Mock]]:
    source = SimpleNamespace(n_frames=2, format=source_format, close=Mock())
    closes: list[Mock] = []

    def materialize(index: int) -> Image.Image:
        if index == 1 and copy_error:
            raise copy_error("frame copy failed")
        image = Image.new("RGB", (10, 10), "white")
        closed = Mock(wraps=image.close)
        monkeypatch.setattr(image, "close", closed)
        closes.append(closed)
        return image

    frames = [SimpleNamespace(width=10, height=10, copy=Mock(side_effect=lambda index=index: materialize(index))) for index in range(2)]
    # 只替换 Pillow 的源和帧；公开入口仍执行限制、复制和关闭，不代表真实 TIFF/PDF 兼容验证。
    monkeypatch.setattr(decoder.Image, "open", lambda _data: source)
    monkeypatch.setattr(decoder.ImageSequence, "Iterator", lambda _source: iter(frames))
    return source, frames, closes


@pytest.mark.parametrize(
    ("limits", "expected", "copies"),
    [
        ({"max_pages": 1}, "image_frame_limit_exceeded", 0),
        ({"max_page_pixels": 50}, "page_pixel_limit_exceeded", 0),
        ({"max_total_pixels": 150}, "document_pixel_limit_exceeded", 1),
    ],
)
def test_limits_close_source_and_materialized_frames_before_next_copy(monkeypatch: pytest.MonkeyPatch, limits: dict, expected: str, copies: int) -> None:
    source, frames, closes = source_frames(monkeypatch)
    with pytest.raises(decoder.DecodeError, match=expected):
        decoder.decode_document(b"image bytes", "image/tiff", **limits)
    source.close.assert_called_once()
    assert sum(frame.copy.call_count for frame in frames) == copies
    assert len(closes) == copies
    for closed in closes:
        closed.assert_called_once()


@pytest.mark.parametrize(
    ("source_format", "content_type", "data", "copy_error", "expected"),
    [
        ("PNG", "image/png", b"image bytes", OSError, "unsupported_or_invalid_image"),
        ("TIFF", "image/jpeg", b"image bytes", ValueError, "invalid_tiff"),
        ("PNG", "image/tiff", b"image bytes", Image.DecompressionBombError, "invalid_tiff"),
        ("PNG", "image/png", b"II*\x00image bytes", OSError, "invalid_tiff"),
    ],
)
def test_known_frame_errors_close_prior_copy_and_preserve_format_mapping(monkeypatch: pytest.MonkeyPatch, source_format: str, content_type: str, data: bytes, copy_error: type[Exception], expected: str) -> None:
    source, frames, closes = source_frames(monkeypatch, source_format=source_format, copy_error=copy_error)
    with pytest.raises(decoder.DecodeError, match=expected) as caught:
        decoder.decode_document(data, content_type)
    assert isinstance(caught.value.__cause__, copy_error)
    source.close.assert_called_once()
    assert [frame.copy.call_count for frame in frames] == [1, 1]
    assert len(closes) == 1
    closes[0].assert_called_once()


def test_success_returns_encoded_pages_and_closes_source_and_copies(monkeypatch: pytest.MonkeyPatch) -> None:
    source, frames, closes = source_frames(monkeypatch)
    pages, detected = decoder.decode_document(b"image bytes", "application/octet-stream")
    assert detected == "image/tiff"
    assert [(page.index, page.width, page.height) for page in pages] == [(1, 10, 10), (2, 10, 10)]
    assert all(page.png.startswith(b"\x89PNG") for page in pages)
    source.close.assert_called_once()
    assert [frame.copy.call_count for frame in frames] == [1, 1]
    assert len(closes) == 2
    assert all(closed.call_count >= 1 for closed in closes)
