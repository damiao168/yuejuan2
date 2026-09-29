from __future__ import annotations

from io import BytesIO

import numpy as np
import zxingcpp
from PIL import Image

MAX_BARCODES_PER_PAGE = 8
MAX_BARCODE_TEXT_LENGTH = 2048


def detect_barcodes(png: bytes) -> list[dict]:
    with Image.open(BytesIO(png)) as source:
        image = np.asarray(source.convert("RGB"))
    # 这里只提取像素位置和原文观察值；码内身份或签名的可信性由后续业务校验。
    observations: list[dict] = []
    for result in zxingcpp.read_barcodes(image)[:MAX_BARCODES_PER_PAGE]:
        text = str(result.text or "")
        if not text or len(text) > MAX_BARCODE_TEXT_LENGTH:
            continue
        position = result.position
        polygon = [
            {"x": int(point.x), "y": int(point.y)}
            for point in (position.top_left, position.top_right, position.bottom_right, position.bottom_left)
        ]
        observations.append({
            "format": str(result.format),
            "text": text,
            "polygon": polygon,
            "orientation": int(getattr(result, "orientation", 0) or 0),
        })
    return observations
