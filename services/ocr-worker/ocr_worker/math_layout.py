from __future__ import annotations

import io
import math
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Protocol

FORMULA_DETECTOR_ACCEPT_SCORE = 0.65
FORMULA_EDGE_INK_THRESHOLD = 0.08


class FormulaLayoutDetector(Protocol):
    model_version: str

    @property
    def initialized(self) -> bool: ...

    def initialize(self) -> None: ...

    def detect(self, image: bytes, min_score: float) -> list[tuple[list[float], float]]: ...


@dataclass(frozen=True)
class FormulaRegion:
    """One formula ROI in source-image pixel coordinates."""

    bbox: tuple[float, float, float, float]
    crop_bbox: tuple[float, float, float, float]
    detector_confidence: float
    crop: bytes
    crop_complete: bool
    edge_ink_ratio: float
    recrop_count: int


class PaddleFormulaLayoutDetector:
    def __init__(self, model_version: str, device: str) -> None:
        self.model_version, self.device, self._model = model_version, device, None

    def initialize(self) -> None:
        if self._model is None:
            from paddleocr import LayoutDetection

            self._model = LayoutDetection(model_name=self.model_version, device=self.device, enable_mkldnn=False)

    @property
    def initialized(self) -> bool:
        return self._model is not None

    def detect(self, image: bytes, min_score: float) -> list[tuple[list[float], float]]:
        self.initialize()
        with tempfile.NamedTemporaryFile(suffix=".png", delete=False) as tmp:
            tmp.write(image)
            path = tmp.name
        try:
            raw = self._model.predict(input=path)
            outputs = list(raw) if not isinstance(raw, (dict, list)) else (raw if isinstance(raw, list) else [raw])
        finally:
            Path(path).unlink(missing_ok=True)
        boxes: list[tuple[list[float], float]] = []
        for output in outputs:
            payload = getattr(output, "json", output)
            payload = payload() if callable(payload) else payload
            if not isinstance(payload, dict):
                continue
            payload = payload.get("res", payload)
            items = payload.get("boxes", payload.get("layout_det_res", [])) if isinstance(payload, dict) else []
            for item in items:
                if not isinstance(item, dict):
                    continue
                label = str(item.get("label", item.get("category_name", ""))).lower().replace("_", " ")
                # Formula numbers are ordinary text anchors; recognizing them
                # as standalone formulas adds calls and harms reading order.
                if label != "formula":
                    continue
                score = float(item.get("score", item.get("confidence", 0)) or 0)
                coords = item.get("coordinate", item.get("bbox"))
                if score < min_score or not isinstance(coords, (list, tuple)) or len(coords) != 4:
                    continue
                x1, y1, a, b = map(float, coords)
                # Paddle 通常给出 x1,y1,x2,y2；后两值未同时大于起点时，兼容分支按宽高解释。
                width, height = (a - x1, b - y1) if a > x1 and b > y1 else (a, b)
                if width > 1 and height > 1:
                    boxes.append(([x1, y1, width, height], score))
        return merge_formula_boxes(boxes)


def open_image(data: bytes) -> Any:
    from PIL import Image

    with Image.open(io.BytesIO(data)) as source:
        source.load()
        return source.convert("RGB")


def extract_formula_regions(
    image_bytes: bytes,
    detector: FormulaLayoutDetector,
    *,
    min_score: float = 0.45,
    padding: int = 12,
    max_padding: int = 96,
    max_padding_height_ratio: float = 0.75,
    edge_threshold: float = FORMULA_EDGE_INK_THRESHOLD,
) -> tuple[tuple[int, int], list[FormulaRegion]]:
    image = open_image(image_bytes)
    try:
        regions = []
        for bbox, score in detector.detect(image_bytes, min_score):
            crop, padded_bbox, edge_ink, recrop_count, crop_complete = adaptive_crop_formula(
                image,
                bbox,
                padding,
                max_padding=max_padding,
                max_padding_height_ratio=max_padding_height_ratio,
                edge_threshold=edge_threshold,
            )
            regions.append(
                FormulaRegion(
                    bbox=tuple(float(value) for value in bbox),
                    crop_bbox=tuple(padded_bbox),
                    detector_confidence=max(0.0, min(1.0, float(score))),
                    crop=crop,
                    crop_complete=crop_complete,
                    edge_ink_ratio=edge_ink,
                    recrop_count=recrop_count,
                )
            )
        return (image.width, image.height), regions
    finally:
        image.close()


def adaptive_crop_formula(
    image: Any,
    bbox: list[float],
    padding: int,
    *,
    max_padding: int = 96,
    max_padding_height_ratio: float = 0.75,
    edge_threshold: float = FORMULA_EDGE_INK_THRESHOLD,
) -> tuple[bytes, list[float], float, int, bool]:
    current_padding = max(0, padding)
    # An absolute allowance that is safe for a large display expression can
    # pull neighbouring prose into a small inline expression. Bound expansion
    # by both the configured ceiling and detector-box height.
    detected_height = max(1.0, abs(float(bbox[3])))
    proportional_limit = math.ceil(detected_height * max(0.25, max_padding_height_ratio))
    max_padding = max(current_padding, min(max_padding, proportional_limit))
    recrop_count = 0
    while True:
        crop, padded_bbox, edge_ink = crop_formula(image, bbox, current_padding)
        if edge_ink <= edge_threshold:
            return crop, padded_bbox, edge_ink, recrop_count, True
        left, top, width, height = padded_bbox
        has_room = left > 0 or top > 0 or left + width < image.width or top + height < image.height
        if not has_room or current_padding >= max_padding:
            return crop, padded_bbox, edge_ink, recrop_count, False
        next_padding = min(max_padding, max(current_padding + 8, current_padding * 2, 16))
        if next_padding == current_padding:
            return crop, padded_bbox, edge_ink, recrop_count, False
        current_padding = next_padding
        recrop_count += 1


def crop_formula(image: Any, bbox: list[float], padding: int) -> tuple[bytes, list[float], float]:
    x, y, width, height = bbox
    left, top = max(0, math.floor(x - padding)), max(0, math.floor(y - padding))
    right, bottom = min(image.width, math.ceil(x + width + padding)), min(image.height, math.ceil(y + height + padding))
    crop = image.crop((left, top, right, bottom))
    gray = crop.convert("L")
    pixels = gray.load()
    band = max(1, min(3, crop.width // 8, crop.height // 8))
    sides = [
        [pixels[xx, yy] < 200 for yy in range(crop.height) for xx in range(band)],
        [pixels[xx, yy] < 200 for yy in range(crop.height) for xx in range(max(0, crop.width - band), crop.width)],
        [pixels[xx, yy] < 200 for yy in range(band) for xx in range(crop.width)],
        [pixels[xx, yy] < 200 for yy in range(max(0, crop.height - band), crop.height) for xx in range(crop.width)],
    ]
    # 用四边中最大的墨迹比例发现截断迹象；这是扩裁启发式，不是完整性证明。
    edge_ink = max((sum(side) / len(side) for side in sides if side), default=1.0)
    output = io.BytesIO()
    crop.save(output, format="PNG")
    crop.close()
    gray.close()
    return output.getvalue(), [float(left), float(top), float(right - left), float(bottom - top)], edge_ink


def merge_formula_boxes(boxes: list[tuple[list[float], float]]) -> list[tuple[list[float], float]]:
    pending = sorted(boxes, key=lambda item: (item[0][1], item[0][0]))
    merged: list[tuple[list[float], float]] = []
    for box, score in pending:
        merged_into_existing = False
        for existing_index, (previous, previous_score) in enumerate(merged):
            intersection = intersection_area(previous, box)
            smaller_area = min(previous[2] * previous[3], box[2] * box[3])
            vertical_overlap = max(
                0,
                min(previous[1] + previous[3], box[1] + box[3]) - max(previous[1], box[1]),
            )
            gap = box[0] - (previous[0] + previous[2])
            duplicate = smaller_area > 0 and intersection / smaller_area >= 0.70
            adjacent_fragment = vertical_overlap >= min(previous[3], box[3]) * 0.6 and -4 <= gap <= 16
            if duplicate or adjacent_fragment:
                x1, y1 = min(previous[0], box[0]), min(previous[1], box[1])
                x2 = max(previous[0] + previous[2], box[0] + box[2])
                y2 = max(previous[1] + previous[3], box[1] + box[3])
                merged[existing_index] = ([x1, y1, x2 - x1, y2 - y1], max(score, previous_score))
                merged_into_existing = True
                break
        if not merged_into_existing:
            merged.append((box, score))
    return sorted(merged, key=lambda item: (item[0][1], item[0][0]))


def intersection_area(left: list[float] | tuple[float, ...], right: list[float] | tuple[float, ...]) -> float:
    width = min(left[0] + left[2], right[0] + right[2]) - max(left[0], right[0])
    height = min(left[1] + left[3], right[1] + right[3]) - max(left[1], right[1])
    return max(0.0, width) * max(0.0, height)


# Compatibility aliases for existing paper-formula imports and tests. New code
# should use the public names above.
_adaptive_crop_formula = adaptive_crop_formula
_crop_formula = crop_formula
_merge_formula_boxes = merge_formula_boxes
_intersection_area = intersection_area
