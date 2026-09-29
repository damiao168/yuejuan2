from __future__ import annotations

import hashlib
import tempfile
from collections import OrderedDict
from collections.abc import Callable, Iterable
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import Any, Protocol

from .engine import OCRBlock, OCREngine
from .formula_validation import normalize_latex
from .math_layout import (
    FORMULA_DETECTOR_ACCEPT_SCORE,
    FormulaLayoutDetector,
    FormulaRegion,
    extract_formula_regions,
    intersection_area,
    open_image,
)


class RegionKind(StrEnum):
    TEXT = "text"
    FORMULA = "formula"
    MIXED = "mixed"
    DIAGRAM = "diagram"
    UNKNOWN = "unknown"


@dataclass(frozen=True)
class FormulaRecognitionResult:
    raw_latex: str
    canonical_latex: str
    confidence: float
    engine: str
    engine_version: str
    status: str
    warnings: tuple[str, ...] = ()


@dataclass(frozen=True)
class FormulaRegionRecognition:
    bbox: tuple[float, float, float, float]
    detector_confidence: float
    crop_complete: bool
    candidates: tuple[FormulaRecognitionResult, ...] = ()
    selected_candidate: int | None = None
    warnings: tuple[str, ...] = ()

    @property
    def formula(self) -> FormulaRecognitionResult | None:
        if self.selected_candidate is None or not 0 <= self.selected_candidate < len(self.candidates):
            return None
        return self.candidates[self.selected_candidate]


@dataclass(frozen=True)
class RoutedRecognitionResult:
    kind: RegionKind
    text_blocks: tuple[OCRBlock, ...] = ()
    formula: FormulaRecognitionResult | None = None
    formula_regions: tuple[FormulaRegionRecognition, ...] = ()
    image_size: tuple[int, int] | None = None
    layout_version: str = ""
    preserve_image_evidence: bool = False
    requires_human_review: bool = False
    reason_code: str = ""


class FormulaEngine(Protocol):
    engine_name: str
    model_version: str

    def initialize(self) -> None: ...

    def recognize_formula(self, image_bytes: bytes) -> FormulaRecognitionResult: ...

    def recognize_formulas(self, images: list[bytes], batch_size: int = 4) -> list[FormulaRecognitionResult]: ...


class RecognitionRouter:
    """Routes already detected regions without creating a second OCR task system."""

    def __init__(
        self,
        *,
        text_engine: OCREngine,
        formula_engine: FormulaEngine | None,
        formula_detector: FormulaLayoutDetector | None = None,
        formula_batch_size: int = 4,
        formula_min_detector_score: float = 0.45,
        formula_accept_detector_score: float = FORMULA_DETECTOR_ACCEPT_SCORE,
        formula_padding: int = 12,
        formula_max_padding: int = 96,
        formula_max_padding_height_ratio: float = 0.75,
        formula_subjects: frozenset[str] = frozenset({"mathematics", "physics", "chemistry"}),
    ) -> None:
        self.text_engine = text_engine
        self.formula_engine = formula_engine
        self.formula_detector = formula_detector
        self.formula_batch_size = max(1, formula_batch_size)
        self.formula_min_detector_score = max(0.0, min(1.0, formula_min_detector_score))
        self.formula_accept_detector_score = max(0.0, min(1.0, formula_accept_detector_score))
        self.formula_padding = max(0, formula_padding)
        self.formula_max_padding = max(self.formula_padding, formula_max_padding)
        self.formula_max_padding_height_ratio = max(0.25, formula_max_padding_height_ratio)
        self.formula_subjects = formula_subjects

    def recognize(self, subject_code: str, kind: RegionKind, image_bytes: bytes) -> RoutedRecognitionResult:
        if not image_bytes:
            return RoutedRecognitionResult(kind=kind, requires_human_review=True, reason_code="empty_region")
        if kind is RegionKind.TEXT:
            blocks = tuple(_recognize_text(self.text_engine, image_bytes))
            return RoutedRecognitionResult(
                kind=kind,
                text_blocks=blocks,
                image_size=_image_size(image_bytes),
                layout_version=getattr(self.formula_detector, "model_version", ""),
                requires_human_review=not blocks,
                reason_code="" if blocks else "empty_text_result",
            )
        if kind is RegionKind.FORMULA:
            if subject_code not in self.formula_subjects:
                return RoutedRecognitionResult(
                    kind=kind,
                    preserve_image_evidence=True,
                    requires_human_review=True,
                    reason_code="formula_not_enabled_for_subject",
                )
            if self.formula_engine is None:
                return RoutedRecognitionResult(kind=kind, requires_human_review=True, reason_code="formula_engine_unavailable")
            try:
                result = self.formula_engine.recognize_formula(image_bytes)
            except Exception:  # noqa: BLE001 - optional model runtimes expose heterogeneous errors.
                return RoutedRecognitionResult(kind=kind, requires_human_review=True, reason_code="formula_engine_failed")
            unsafe = result.status != "recognized" or not result.canonical_latex or result.confidence <= 0
            return RoutedRecognitionResult(
                kind=kind,
                formula=result,
                image_size=_image_size(image_bytes),
                layout_version=getattr(self.formula_detector, "model_version", ""),
                requires_human_review=unsafe,
                reason_code="ambiguous_formula" if unsafe else "",
            )
        if kind is RegionKind.MIXED:
            return self._recognize_mixed(subject_code, image_bytes)
        if kind is RegionKind.DIAGRAM:
            return RoutedRecognitionResult(kind=kind, preserve_image_evidence=True, requires_human_review=True, reason_code="diagram_requires_review")
        return RoutedRecognitionResult(kind=RegionKind.UNKNOWN, preserve_image_evidence=True, requires_human_review=True, reason_code="unknown_region")

    def _recognize_mixed(self, subject_code: str, image_bytes: bytes) -> RoutedRecognitionResult:
        try:
            text_blocks = tuple(_recognize_text(self.text_engine, image_bytes))
        except Exception:  # noqa: BLE001 - Paddle adapters expose heterogeneous errors.
            text_blocks = ()
        if subject_code not in self.formula_subjects:
            return RoutedRecognitionResult(
                kind=RegionKind.MIXED,
                text_blocks=text_blocks,
                image_size=_image_size(image_bytes),
                preserve_image_evidence=True,
                requires_human_review=True,
                reason_code="formula_not_enabled_for_subject",
            )
        if self.formula_detector is None:
            return RoutedRecognitionResult(
                kind=RegionKind.MIXED,
                text_blocks=text_blocks,
                image_size=_image_size(image_bytes),
                preserve_image_evidence=True,
                requires_human_review=True,
                reason_code="formula_layout_detector_unavailable",
            )
        try:
            image_size, detected = extract_formula_regions(
                image_bytes,
                self.formula_detector,
                min_score=self.formula_min_detector_score,
                padding=self.formula_padding,
                max_padding=self.formula_max_padding,
                max_padding_height_ratio=self.formula_max_padding_height_ratio,
            )
        except Exception:  # noqa: BLE001 - layout runtimes expose heterogeneous errors.
            return RoutedRecognitionResult(
                kind=RegionKind.MIXED,
                text_blocks=text_blocks,
                image_size=_image_size(image_bytes),
                preserve_image_evidence=True,
                requires_human_review=True,
                reason_code="formula_layout_failed",
            )
        if not detected:
            return RoutedRecognitionResult(
                kind=RegionKind.MIXED,
                text_blocks=text_blocks,
                image_size=image_size,
                layout_version=getattr(self.formula_detector, "model_version", ""),
                preserve_image_evidence=True,
                requires_human_review=True,
                reason_code="formula_region_not_detected",
            )

        eligible = [
            index
            for index, region in enumerate(detected)
            if region.detector_confidence >= self.formula_accept_detector_score and region.crop_complete
        ]
        recognized: dict[int, FormulaRecognitionResult] = {}
        recognition_failed = False
        if eligible and self.formula_engine is not None:
            try:
                batch = getattr(self.formula_engine, "recognize_formulas", None)
                if callable(batch):
                    predictions = batch([detected[index].crop for index in eligible], batch_size=self.formula_batch_size)
                else:
                    predictions = [self.formula_engine.recognize_formula(detected[index].crop) for index in eligible]
                if len(predictions) != len(eligible):
                    raise RuntimeError("formula engine returned an incomplete mixed-region batch")
                recognized = dict(zip(eligible, predictions, strict=True))
            except Exception:  # noqa: BLE001 - optional formula runtimes expose heterogeneous errors.
                recognition_failed = True

        formula_regions: list[FormulaRegionRecognition] = []
        unsafe = False
        for index, region in enumerate(detected):
            prediction = recognized.get(index)
            warnings = _formula_region_warnings(
                region,
                prediction,
                accept_detector_score=self.formula_accept_detector_score,
                engine_available=self.formula_engine is not None,
                recognition_failed=recognition_failed,
            )
            selected = 0 if prediction is not None else None
            if prediction is None or prediction.status != "recognized" or not prediction.canonical_latex:
                unsafe = True
            formula_regions.append(
                FormulaRegionRecognition(
                    bbox=region.bbox,
                    detector_confidence=region.detector_confidence,
                    crop_complete=region.crop_complete,
                    candidates=(prediction,) if prediction is not None else (),
                    selected_candidate=selected,
                    warnings=warnings,
                )
            )
        reconciled_text = _reconcile_text_blocks(text_blocks, tuple(formula_regions))
        reason_code = ""
        if self.formula_engine is None:
            reason_code = "formula_engine_unavailable"
        elif recognition_failed:
            reason_code = "formula_engine_failed"
        elif unsafe:
            reason_code = "ambiguous_formula"
        return RoutedRecognitionResult(
            kind=RegionKind.MIXED,
            text_blocks=reconciled_text,
            formula_regions=tuple(formula_regions),
            image_size=image_size,
            layout_version=getattr(self.formula_detector, "model_version", ""),
            preserve_image_evidence=unsafe,
            requires_human_review=unsafe or (not reconciled_text and not recognized),
            reason_code=reason_code,
        )


def _recognize_text(engine: OCREngine, image_bytes: bytes) -> list[OCRBlock]:
    recognize_region = getattr(engine, "recognize_region", None)
    if callable(recognize_region):
        return list(recognize_region(image_bytes))
    return list(engine.recognize(image_bytes))


def _image_size(image_bytes: bytes) -> tuple[int, int] | None:
    try:
        image = open_image(image_bytes)
    except Exception:  # noqa: BLE001 - invalid image is reported by the routed result.
        return None
    try:
        return image.width, image.height
    finally:
        image.close()


def _formula_region_warnings(
    region: FormulaRegion,
    prediction: FormulaRecognitionResult | None,
    *,
    accept_detector_score: float,
    engine_available: bool,
    recognition_failed: bool,
) -> tuple[str, ...]:
    warnings = []
    if region.detector_confidence < accept_detector_score:
        warnings.append("low_detector_confidence")
    if not region.crop_complete:
        warnings.append("incomplete_formula_crop")
    if not engine_available:
        warnings.append("formula_engine_unavailable")
    elif recognition_failed:
        warnings.append("formula_engine_failed")
    elif prediction is not None:
        warnings.extend(prediction.warnings)
        if prediction.status != "recognized" or not prediction.canonical_latex:
            warnings.append("ambiguous_formula")
    return tuple(dict.fromkeys(warnings))


def _reconcile_text_blocks(
    blocks: tuple[OCRBlock, ...],
    formula_regions: tuple[FormulaRegionRecognition, ...],
) -> tuple[OCRBlock, ...]:
    """Remove formula pixels from OCR lines so evidence is never duplicated.

    Paddle OCR exposes a line box rather than character boxes. For a partial
    overlap we therefore split text proportionally along the line. The
    resulting residual text is deliberately lower confidence and its boxes do
    not overlap the detected formula ROI.
    """

    result = list(blocks)
    for region in formula_regions:
        next_result: list[OCRBlock] = []
        for block in result:
            next_result.extend(_subtract_formula_from_text(block, region.bbox))
        result = next_result
    return tuple(block for block in result if block.text.strip())


def _subtract_formula_from_text(block: OCRBlock, formula_box: tuple[float, float, float, float]) -> list[OCRBlock]:
    if len(block.bbox) != 4 or len(formula_box) != 4:
        return [block]
    text_box = tuple(float(value) for value in block.bbox)
    tx, ty, tw, th = text_box
    fx, fy, fw, fh = formula_box
    if tw <= 0 or th <= 0 or fw <= 0 or fh <= 0 or intersection_area(text_box, formula_box) <= 0:
        return [block]
    vertical_overlap = min(ty + th, fy + fh) - max(ty, fy)
    if vertical_overlap / min(th, fh) < 0.55:
        return [block]

    overlap_left, overlap_right = max(tx, fx), min(tx + tw, fx + fw)
    if overlap_right <= overlap_left:
        return [block]
    text = block.text
    start_index = max(0, min(len(text), round((overlap_left - tx) / tw * len(text))))
    end_index = max(start_index, min(len(text), round((overlap_right - tx) / tw * len(text))))
    confidence = max(0.0, min(1.0, float(block.confidence))) * 0.85
    fragments: list[OCRBlock] = []
    left_text = text[:start_index].strip(" \t=:：,，;；")
    left_width = max(0.0, overlap_left - tx)
    if left_text and left_width > 0:
        fragments.append(OCRBlock(left_text, [tx, ty, left_width, th], confidence))
    right_text = text[end_index:].strip(" \t=:：,，;；")
    right_width = max(0.0, tx + tw - overlap_right)
    if right_text and right_width > 0:
        fragments.append(OCRBlock(right_text, [overlap_right, ty, right_width, th], confidence))
    return fragments


class PaddleFormulaNetEngine:
    engine_name = "paddle-formula"

    def __init__(
        self,
        *,
        model_version: str = "PP-FormulaNet_plus-M",
        device: str = "cpu",
        cache_size: int = 2048,
    ) -> None:
        if model_version not in {"PP-FormulaNet_plus-M", "PP-FormulaNet_plus-L"}:
            raise ValueError("unsupported PP-FormulaNet model")
        self.model_version = model_version
        self.device = device
        self._pipeline: Any = None
        self._cache_size = max(0, cache_size)
        self._cache: OrderedDict[str, FormulaRecognitionResult] = OrderedDict()

    def initialize(self) -> None:
        self._load()

    @property
    def initialized(self) -> bool:
        return self._pipeline is not None

    def recognize_formula(self, image_bytes: bytes) -> FormulaRecognitionResult:
        if not image_bytes:
            return FormulaRecognitionResult("", "", 0, self.engine_name, self.model_version, "failed", ("empty_image",))
        return self.recognize_formulas([image_bytes], batch_size=1)[0]

    def recognize_formulas(self, images: list[bytes], batch_size: int = 4) -> list[FormulaRecognitionResult]:
        if batch_size < 1:
            raise ValueError("formula batch_size must be positive")
        if not images:
            return []
        results: list[FormulaRecognitionResult | None] = [None] * len(images)
        misses: dict[str, list[int]] = {}
        miss_images: dict[str, bytes] = {}
        for index, image in enumerate(images):
            if not image:
                results[index] = FormulaRecognitionResult(
                    "", "", 0, self.engine_name, self.model_version, "failed", ("empty_image",),
                )
                continue
            key = hashlib.sha256(image).hexdigest()
            cached = self._cache.get(key)
            if cached is not None:
                self._cache.move_to_end(key)
                results[index] = cached
                continue
            # 同批相同裁剪按内容哈希合并推理，结果仍回填到每个原始位置。
            misses.setdefault(key, []).append(index)
            miss_images.setdefault(key, image)

        keys = list(misses)
        if keys:
            predicted = self._predict_uncached([miss_images[key] for key in keys], batch_size=batch_size)
            if len(predicted) != len(keys):
                raise RuntimeError(f"formula batch returned {len(predicted)} results for {len(keys)} inputs")
            for key, result in zip(keys, predicted, strict=True):
                for index in misses[key]:
                    results[index] = result
                if self._cache_size and result.status == "recognized":
                    self._cache[key] = result
                    self._cache.move_to_end(key)
                    while len(self._cache) > self._cache_size:
                        self._cache.popitem(last=False)
        return [result if result is not None else FormulaRecognitionResult(
            "", "", 0, self.engine_name, self.model_version, "failed", ("missing_batch_result",),
        ) for result in results]

    def _predict_uncached(self, images: list[bytes], *, batch_size: int) -> list[FormulaRecognitionResult]:
        pipeline = self._load()
        image_paths: list[str] = []
        try:
            for image in images:
                with tempfile.NamedTemporaryFile(suffix=".png", delete=False) as tmp:
                    tmp.write(image)
                    image_paths.append(tmp.name)
            try:
                raw = pipeline.predict(input=image_paths, batch_size=min(batch_size, len(image_paths)))
            except TypeError:
                # Compatibility with older PaddleOCR adapters. This path keeps
                # correctness but intentionally remains visible in benchmarks.
                raw = [next(iter(pipeline.predict(input=path)), {}) for path in image_paths]
            predictions = _parse_formula_predictions(raw)
        finally:
            for image_path in image_paths:
                Path(image_path).unlink(missing_ok=True)
        return [
            FormulaRecognitionResult(
                raw_latex=latex,
                canonical_latex=canonicalize_latex(latex),
                confidence=confidence,
                engine=self.engine_name,
                engine_version=self.model_version,
                status="recognized" if canonicalize_latex(latex) else "failed",
            )
            for latex, confidence in predictions
        ]

    def _load(self) -> Any:
        if self._pipeline is not None:
            return self._pipeline
        try:
            from paddleocr import FormulaRecognition
        except ImportError as exc:
            raise RuntimeError("PaddleOCR formula runtime is not installed") from exc
        # Paddle's oneDNN executor currently cannot run every PIR attribute
        # emitted by FormulaNet/Layout models on CPU. The plain executor is
        # slower but deterministic and avoids a task-ending NotImplementedError.
        self._pipeline = FormulaRecognition(model_name=self.model_version, device=self.device, enable_mkldnn=False)
        return self._pipeline


class UniMERNetEngine:
    """Challenger adapter; deployment must inject a versioned, governed predictor."""

    engine_name = "unimernet"

    def __init__(self, *, model_version: str, predictor: Callable[[bytes], tuple[str, float]] | None = None) -> None:
        self.model_version = model_version
        self._predictor = predictor

    def initialize(self) -> None:
        if self._predictor is None:
            raise RuntimeError("UniMERNet predictor is not configured")

    def recognize_formula(self, image_bytes: bytes) -> FormulaRecognitionResult:
        self.initialize()
        assert self._predictor is not None
        latex, confidence = self._predictor(image_bytes)
        canonical = canonicalize_latex(latex)
        return FormulaRecognitionResult(latex, canonical, confidence, self.engine_name, self.model_version, "recognized" if canonical else "failed")


def canonicalize_latex(value: str) -> str:
    return normalize_latex(value)


def _parse_formula_prediction(raw: object) -> tuple[str, float]:
    predictions = _parse_formula_predictions(raw)
    return predictions[0] if predictions else ("", 0.0)


def _parse_formula_predictions(raw: object) -> list[tuple[str, float]]:
    pages = list(raw) if isinstance(raw, Iterable) and not isinstance(raw, (dict, str, bytes)) else [raw]
    predictions: list[tuple[str, float]] = []
    for page in pages:
        payload = getattr(page, "json", page)
        if callable(payload):
            payload = payload()
        if not isinstance(payload, dict):
            predictions.append(("", 0.0))
            continue
        nested = payload.get("res") if isinstance(payload.get("res"), dict) else payload
        latex = nested.get("rec_formula") or nested.get("formula") or nested.get("latex")
        if isinstance(latex, str) and latex.strip():
            score = nested.get("rec_score", nested.get("score", nested.get("confidence", 0.0)))
            predictions.append((latex, float(score or 0.0)))
        else:
            predictions.append(("", 0.0))
    return predictions
