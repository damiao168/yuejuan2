from __future__ import annotations

# 本阶段持有逐页下载和原图资源；输出仅带裁片字节、页身份与页内 ROI 索引。
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from .paper_formula_projection import _progress
from .runner import _LeaseHeartbeat


@dataclass(frozen=True)
class FormulaCropStage:
    api: Any
    detector: Any
    padding: int
    max_padding_height_ratio: float
    runtime_plan: dict[str, Any]

    def detect_formula_crops(
        self,
        pages: list[dict[str, Any]],
        policy: dict[str, Any],
        heartbeat: _LeaseHeartbeat,
        max_padding: int,
        edge_threshold: float,
        page_total: int,
        *,
        open_image: Callable[[bytes], Any],
        adaptive_crop: Callable[..., Any],
    ) -> list[dict[str, Any]]:
        crops: list[dict[str, Any]] = []
        if not self.detector.initialized:
            heartbeat.set_progress(
                _progress(
                    "formula_detection",
                    "model_loading",
                    0,
                    page_total,
                    "page",
                    self.detector.model_version,
                    "正在从本地模型缓存加载公式区域检测模型",
                    cold_start=True,
                    **self.runtime_plan,
                ),
                flush=True,
            )
            self.detector.initialize()
        heartbeat.set_progress(
            _progress(
                "formula_detection",
                "detecting",
                0,
                page_total,
                "page",
                self.detector.model_version,
                "公式区域检测模型已就绪，正在检测页面",
                cold_start=False,
                **self.runtime_plan,
            ),
            flush=True,
        )
        for page_index, page in enumerate(pages, 1):
            heartbeat.raise_if_failed()
            image_bytes = self.api.download(str(page["download_url"]))
            image = open_image(image_bytes)
            min_score = float(policy.get("min_detector_score", 0.45))
            padding = int(policy.get("roi_padding_pixels", self.padding))
            for index, (bbox, detector_score) in enumerate(self.detector.detect(image_bytes, min_score), 1):
                crop, padded_bbox, edge_ink, recrop_count, crop_complete = adaptive_crop(
                    image,
                    bbox,
                    padding,
                    max_padding=max(max_padding, padding),
                    max_padding_height_ratio=self.max_padding_height_ratio,
                    edge_threshold=edge_threshold,
                )
                crops.append(
                    {
                        "page": page,
                        "index": index,
                        "crop": crop,
                        "bbox": padded_bbox,
                        "detector_score": detector_score,
                        "edge_ink": edge_ink,
                        "recrop_count": recrop_count,
                        "crop_complete": crop_complete,
                        "aspect_ratio": padded_bbox[2] / max(padded_bbox[3], 1.0),
                    }
                )
            image.close()
            heartbeat.set_progress(
                _progress(
                    "formula_detection",
                    "detecting",
                    page_index,
                    page_total,
                    "page",
                    self.detector.model_version,
                    f"已检测 {page_index}/{page_total} 页，发现 {len(crops)} 个公式区域",
                    page_no=page_index,
                    page_total=page_total,
                    **self.runtime_plan,
                ),
                flush=True,
            )
        return crops
