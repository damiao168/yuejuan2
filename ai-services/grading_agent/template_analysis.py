"""多模态理解完整页面，OpenCV 仅提供线框提示和保守边界精修。

模型可发现无框区域和漏检区域，题号及内容一致的结果可自动对应，仍不发布模板。
来源显示坐标不变；题号冲突及供应商失败保留纠错原因，缺少 OCR 不否定模型结果。
"""

import base64
import copy
import hashlib
import os
import time

import cv2
import numpy as np

from .answer_sheet_vision import (
    ALGORITHM_VERSION as VISION_VERSION,
)
from .answer_sheet_vision import (
    VisionInputError,
    decode_display_page,
    detect_printed_frames,
    snap_to_printed_frame,
)
from .errors import AgentError
from .model import describe_model_usage, sum_model_usage
from .template_analysis_barcode_discovery import (
    BARCODE_DISCOVERY_VERSION,
    discover_barcode_regions,
)
from .template_analysis_barcodes import (
    _align_existing_discovered_barcodes,
    _barcode_repair_hints,
)
from .template_analysis_candidates import (
    _discovered_id,
    _finalize_candidate_reasons,
    _geometry_fallback,
    _messages,
    _normalize,
    _page_evidence,
    _reason,
    _refine,
    _restore_coarse_registration,
    _review_objective_item_conflicts,
    _review_region_conflicts,
    _semantic_failure_reason,
    _validate,
    _validate_bbox,
)
from .template_analysis_coarse import (
    COARSE_CONTRACT_VERSION,
    coarse_model_output_schema,
    expand_coarse_output,
)
from .template_analysis_context import (
    CONTEXT_FILTER_VERSION,
)
from .template_analysis_coverage import COVERAGE_VERSION, review_template_page_coverage
from .template_analysis_diagnostics import AnalysisTrace
from .template_analysis_fill_groups import (
    FILL_GROUP_VERSION,
    discover_fill_grading_groups,
)
from .template_analysis_fill_regions import FILL_REGION_VERSION, discover_fill_regions
from .template_analysis_final_review import apply_page_coverage
from .template_analysis_forbidden import (
    FORBIDDEN_REGION_VERSION,
    review_forbidden_regions,
)
from .template_analysis_frame_alignment import (
    FRAME_ALIGNMENT_VERSION,
)
from .template_analysis_identity_alignment import (
    IDENTITY_ALIGNMENT_VERSION,
    align_identity_fields,
    align_initial_identity_fields,
)
from .template_analysis_observations import (
    OBSERVATION_REVIEW_VERSION,
)
from .template_analysis_omr import OMR_LAYOUT_VERSION, populate_objective_items
from .template_analysis_option_alignment import (
    OPTION_ALIGNMENT_VERSION,
)
from .template_analysis_prompt import prompt_version
from .template_analysis_quality import (
    SYNTHETIC_PARTITION_REASON,
    review_synthetic_page_partition,
)
from .template_analysis_registration import (
    REGISTRATION_VERSION,
    prepare_registration,
    rectify_registration,
    region_within_registration,
    transform_registration_bbox,
)
from .template_analysis_repair import bounded_initial_model, repair_template_candidates
from .template_analysis_repair_merge import (
    _apply_repair_batches as _merge_repair_batches,
)
from .template_analysis_repair_merge import (
    _merge_local_fill_regions,
    _review_question_association_conflicts,
)
from .template_analysis_schema import (
    ALGORITHM_VERSION,
    MAX_CANDIDATES,
    MAX_PAGE_BYTES,
    PROMPT_VERSION,
    SCHEMA_VERSION,
    model_output_schema,
    request_schema,
)
from .template_analysis_semantics import (
    SEMANTIC_VERSION,
    LocalContentEvidence,
)

_REGION_CONFLICT_VERSION = "visible-regions-after-exclusion-v2"


class TemplateAnalyzer:
    def __init__(self, model=None, content_evidence_factory=LocalContentEvidence, *, prompt_variant=None):
        self.model = model
        self.content_evidence_factory = content_evidence_factory
        # 默认使用带内容观察的多模态识别；历史版本仅供部署回退，页面资料不能切换版本。
        self.prompt_variant = (os.getenv("EDUGRADE_TEMPLATE_ANALYSIS_PROMPT_VARIANT", "v7-evidence")
                               if prompt_variant is None else prompt_variant)
        if not isinstance(self.prompt_variant, str) or self.prompt_variant not in {"v6", "v7-text", "v7-evidence", "v8-layout"}:
            raise AgentError("model_unavailable", "template prompt configuration is invalid", status=503)
        self.versions = {"schema": SCHEMA_VERSION,
                         "prompt": PROMPT_VERSION if self.prompt_variant == "v6" else prompt_version(self.prompt_variant),
                         "algorithm": ALGORITHM_VERSION if self.prompt_variant == "v6" else "multimodal-layout-with-content-evidence-v7",
                         "geometry": VISION_VERSION, "semantic": SEMANTIC_VERSION,
                         "fill_regions": FILL_REGION_VERSION, "forbidden_regions": FORBIDDEN_REGION_VERSION,
                         "region_conflicts": _REGION_CONFLICT_VERSION}
        if self.prompt_variant in {"v7-evidence", "v8-layout"}:
            self.versions["observations"] = OBSERVATION_REVIEW_VERSION
        if self.prompt_variant == "v8-layout":
            self.versions["algorithm"] = "multimodal-layout-with-local-repair-v8"
            self.versions["barcode_discovery"] = BARCODE_DISCOVERY_VERSION
            self.versions["coarse_contract"] = COARSE_CONTRACT_VERSION
            self.versions["coarse_frame_alignment"] = FRAME_ALIGNMENT_VERSION
            self.versions["option_alignment"] = OPTION_ALIGNMENT_VERSION
            self.versions["identity_alignment"] = IDENTITY_ALIGNMENT_VERSION
            self.versions["repair_context"] = CONTEXT_FILTER_VERSION
            self.versions["coverage"] = COVERAGE_VERSION
            self.versions["registration"] = REGISTRATION_VERSION
            self.versions["fill_grading_groups"] = FILL_GROUP_VERSION
            self.versions["omr_layout"] = OMR_LAYOUT_VERSION

    def analyze(self, payload):
        """单页分析不缓存、不发布；调用方须核对来源和模板 revision 的适用性。"""
        started = time.monotonic()
        payload = copy.deepcopy(payload)
        request_id = payload.get("request_id", "") if isinstance(payload, dict) else ""
        _validate(payload, request_schema(), "invalid_request", request_id)
        page = payload["page"]
        try:
            raw = base64.b64decode(page["data_base64"], validate=True)
        except ValueError as exc:
            raise AgentError("invalid_request", "page data is invalid", status=400, request_id=request_id) from exc
        if not raw or len(raw) > MAX_PAGE_BYTES or hashlib.sha256(raw).hexdigest() != page["sha256"]:
            raise AgentError("invalid_request", "page checksum or size is invalid", status=400, request_id=request_id)
        try:
            image = decode_display_page(raw, page)
        except VisionInputError as exc:
            raise AgentError("invalid_request", "page display data or dimensions are invalid",
                             status=400, request_id=request_id) from exc
        source_page = dict(page)
        source_image = image
        registration = prepare_registration(image, page["page_no"])
        projection = rectify_registration(image, registration) if self.prompt_variant == "v8-layout" else None
        if projection and projection["rectified"]:
            # 定位点先统一语义视图，局部格子和横线仍在原像素测量，避免校正重采样抹掉细线。
            # 两张图各有实际哈希；模型粗框只逆变换一次，此后的测量、裁图和保存统一用来源坐标。
            image = source_image.copy()
            mask = np.zeros(image.shape[:2], dtype=np.uint8)
            quad = np.int32([[round(point["x"] * page["width"]), round(point["y"] * page["height"])]
                             for point in registration["stable_quad"]])
            cv2.fillConvexPoly(mask, quad, 255)
            image[mask == 0] = 255
        candidates = {}
        for candidate in payload["candidates"]:
            candidate_id = candidate["candidate_id"]
            if candidate_id in candidates:
                raise AgentError("invalid_request", "candidate IDs must be unique", status=400, request_id=request_id)
            _validate_bbox(candidate["bbox"], "invalid_request", request_id)
            if projection and projection["rectified"] and not region_within_registration(candidate["bbox"], registration):
                continue
            candidates[candidate_id] = candidate

        frames = detect_printed_frames(image, page["page_no"])
        if projection and projection["rectified"]:
            frames = [frame for frame in frames if region_within_registration(
                frame["bbox"], registration)]
        for candidate in candidates.values():
            bbox, refs, _ = snap_to_printed_frame(candidate["bbox"], frames)
            candidate["bbox"] = bbox
            candidate["evidence_refs"] = list(dict.fromkeys([*candidate["evidence_refs"], *refs]))
        for frame in frames:
            if len(candidates) >= MAX_CANDIDATES:
                break
            if not any(frame["bbox"] == candidate["bbox"] for candidate in candidates.values()):
                candidates[frame["candidate_id"]] = frame
        payload["candidates"] = list(candidates.values())
        initial_payload = payload
        if projection and projection["rectified"]:
            initial_payload = copy.deepcopy(payload)
            corrected = projection["image"].copy()
            mask = np.zeros(image.shape[:2], dtype=np.uint8)
            quad = np.int32([[round(point["x"] * page["width"]), round(point["y"] * page["height"])]
                             for point in projection["registration"]["stable_quad"]])
            cv2.fillConvexPoly(mask, quad, 255)
            corrected[mask == 0] = 255
            encoded = cv2.imencode(".png", cv2.cvtColor(corrected, cv2.COLOR_RGB2BGR))[1].tobytes()
            initial_payload["page"].update(media_type="image/png", data_base64=base64.b64encode(encoded).decode(),
                                            sha256=hashlib.sha256(encoded).hexdigest())
            for candidate in initial_payload["candidates"]:
                candidate["bbox"] = transform_registration_bbox(
                    candidate["bbox"], projection["source_to_rectified"], page["width"], page["height"])

        trace = AnalysisTrace(payload, dict(self.versions), self.model)
        trace.metadata["registration"] = {"status": registration["status"],
                                           "rectified": bool(projection and projection["rectified"]),
                                           "source_image_sha256": source_page["sha256"],
                                           "analysis_image_sha256": initial_payload["page"]["sha256"]}
        with trace.completion():
            result = self._analyze_candidates(payload, image, page, candidates, frames, trace, started,
                                              initial_payload=initial_payload, initial_projection=projection)
            if registration["status"] == "detected":
                result["candidates"] = [row for row in result["candidates"]
                                        if region_within_registration(row["proposed_bbox"], registration)]
            result["registration"] = registration
            return result

    def _analyze_candidates(self, payload, image, page, candidates, frames, trace, started, *, initial_payload=None, initial_projection=None):
        request_id = payload["request_id"]
        output = {"candidates": []}
        usage = {}
        semantic_status = "not_configured"
        reason_codes = []
        if len(frames) >= MAX_CANDIDATES:
            reason_codes.append("GEOMETRY_CANDIDATE_LIMIT_REACHED")
        semantic_error = None
        attempted = False
        content_evidence = None
        # 整页发现只请求一次；OpenCV 没有找到框，也不能跳过视觉模型或假定没有答题区。
        if self.model is not None:
            # 首轮与局部阶段共享整次预算；不能沿用通用解析器的 230 秒等待而超过网关时限。
            # 网关给单页约九十秒；给首轮、局部复判、整页查漏和一次收尾修复各留有界时间。
            initial_model = (bounded_initial_model(self.model, deadline=started + 82)
                             if self.prompt_variant == "v8-layout" else self.model)
            schema = model_output_schema(list(candidates), page["page_no"],
                                         include_observations=self.prompt_variant in {"v7-evidence", "v8-layout"})
            if self.prompt_variant == "v8-layout":
                schema = coarse_model_output_schema()
            messages = _messages(initial_payload or payload, self.prompt_variant)
            trace.bind_request(messages, schema, prompt_variant=self.prompt_variant)
            try:
                if initial_model is None:
                    raise AgentError("model_unavailable", "template model time budget is unavailable", status=503)
                with trace.active(), initial_model.session(request_id):
                    attempted = True
                    trace.metadata["request_count"] = 1
                    try:
                        output = initial_model.request_structured(
                            request_id, messages, schema, "answer_sheet_template_analysis",
                        )
                        if not trace.metadata["attempts"]:
                            trace.capture(output)
                        # 适配器通常已校验 schema；这里仍校验，避免替代适配器跳过来源边界。
                        _validate(output, schema, "model_output_invalid", request_id)
                        if self.prompt_variant == "v8-layout":
                            if initial_projection and initial_projection["rectified"]:
                                output = _restore_coarse_registration(output, initial_projection, page)
                            output = expand_coarse_output(output, payload)
                            # 多模态先给用途和本页可见题号，OMR 直接实测格子与唯一题号排布。
                            # 透视副本不再反复重采样小格，选项保留原像素测量后投到统一坐标。
                            content_evidence = self.content_evidence_factory(image, page)
                            reader = getattr(content_evidence, "static_tokens", None)
                            trace.metadata["omr_layout"] = populate_objective_items(
                                output["candidates"], image, payload,
                                tokens=reader() if callable(reader) else (), frames=frames, deadline=started + 58)
                            measured_ids = set()
                            measured_refs = {}
                            for index in trace.metadata["omr_layout"]["completed_indices"]:
                                candidate = output["candidates"][index]
                                measured_id = _discovered_id(candidate, payload)
                                measured_ids.add(measured_id)
                                # normalize 校验模型引用只接受来源页。独立 OMR 引用在其后由服务追加，
                                # 不能为了本地测量放开模型自报任意证据的边界。
                                measured_refs[measured_id] = [ref for ref in candidate["evidence_refs"]
                                                              if ref != _page_evidence(payload)]
                                candidate["evidence_refs"] = [_page_evidence(payload)]
                        output["candidates"] = _normalize(output["candidates"], candidates, payload)
                        if self.prompt_variant == "v8-layout":
                            for candidate in output["candidates"]:
                                if candidate["candidate_id"] in measured_ids:
                                    _reason(candidate, "LOCAL_OBJECTIVE_OMR_LAYOUT_VERIFIED")
                                    candidate["evidence_refs"] = list(dict.fromkeys([
                                        *candidate["evidence_refs"], *measured_refs[candidate["candidate_id"]],
                                        f"local-omr:{OMR_LAYOUT_VERSION}:{page['sha256'][:16]}:{page['page_no']}" ]))
                        semantic_status = "completed"
                    finally:
                        reader = getattr(initial_model, "last_usage", None)
                        if callable(reader):
                            usage = reader()
            except AgentError as exc:
                # 排队失败也回退；供应商失败和无效结果不允许留下部分模型关联。
                output = {"candidates": []}
                semantic_status = "failed"
                semantic_error = {"code": exc.code, "retryable": exc.retryable}
                semantic_error.update(exc.upstream_diagnostics())
                reason_codes.append("SEMANTIC_ANALYSIS_FAILED")
                reason_codes.append(_semantic_failure_reason(exc))
        elif self.model is None:
            reason_codes.append("SEMANTIC_MODEL_NOT_CONFIGURED")
        if not candidates:
            reason_codes.append("NO_GEOMETRY_CANDIDATES")
        if semantic_status == "completed":
            normalized = output["candidates"]
        else:
            missing_reason = "SEMANTIC_ANALYSIS_FAILED" if semantic_status == "failed" else "SEMANTIC_ANALYSIS_REQUIRED"
            normalized = _geometry_fallback(candidates, payload, missing_reason)
        _refine(normalized, frames, page)
        if semantic_status == "completed":
            _review_objective_item_conflicts(normalized, payload)
            content_evidence = content_evidence or self.content_evidence_factory(image, page)
            if self.prompt_variant == "v8-layout":
                reader = getattr(content_evidence, "static_tokens", None)
                aligned = align_initial_identity_fields(normalized, image, page,
                    tokens=reader() if callable(reader) else (), deadline=started + 58)
                for index in aligned["aligned_indices"]:
                    _reason(normalized[index], "LOCAL_IDENTITY_FIELD_STRUCTURE_ALIGNED")
                    normalized[index]["evidence_refs"].append(
                        f"local-identity:{IDENTITY_ALIGNMENT_VERSION}:{page['sha256'][:16]}:{page['page_no']}")
                trace.metadata["identity_alignment"] = {
                    "version": aligned["version"], "aligned_fields": len(aligned["aligned_indices"]),
                    "label_read_count": aligned["label_read_count"], "window_count": aligned["window_count"],
                    "reason_codes": aligned["reason_codes"]}
            content_evidence.review(normalized, payload)
            if self.prompt_variant == "v8-layout":
                # 有实测题号和横线的填空拆分先解决多题父框，再安排有限的模型复判，
                # 避免对已经有独立证据的同一合题框重复花一次调用。
                fill_labels = [label for label in payload.get("fill_blank_question_labels", [])
                               if label in payload["question_labels"] and label not in payload.get("objective_question_labels", [])]
                local, recovery_reasons = discover_fill_regions(
                    image, page, frames, fill_labels,
                    observed_fill_labels={label for row in normalized for label in row.get("_fill_parent_labels", [])})
                reason_codes.extend(recovery_reasons)
                _merge_local_fill_regions(normalized, local, page)
                hints, hint_reasons = discover_barcode_regions(
                    image, page, frames, ocr=lambda _image: (content_evidence.static_tokens(), None))
                aligned_codes = _align_existing_discovered_barcodes(hints, normalized)
                if aligned_codes:
                    content_evidence.review(aligned_codes, payload)
                normalized.extend(_barcode_repair_hints(hints, normalized, page))
                trace.metadata["barcode_discovery"] = {"hint_count": len(hints), "reason_codes": hint_reasons}
                # 先在原像素裁图中纠正可疑用途及密集坐标，再做最终覆盖/空间校验。失败保留原问题，不覆盖可用结果。
                with trace.active():
                    repaired = repair_template_candidates(
                        self.model, payload, image, normalized, deadline=min(started + 58, time.monotonic() + 40))
                usage = sum_model_usage(usage, repaired["usage"])
                attempted_count = repaired["metadata"]["request_count"]
                trace.metadata["local_repair"] = repaired["metadata"]
                trace.metadata["request_count"] = int(attempted) + attempted_count
                trace.metadata["local_repair"]["applications"] = _apply_repair_batches(
                    normalized, repaired["batches"], payload, frames, page, content_evidence,
                    barcode_hints=hints, deadline=started + 58)
                # 码定位只是补漏线索；未通过模型复判的线索不变成教师必须处理的区域，无条码页也不报缺失。
                normalized[:] = [row for row in normalized if not row.get("_barcode_discovery_hint")]
                reason_codes.extend(repaired["metadata"].get("reason_codes", []))
        if semantic_status == "completed" and review_synthetic_page_partition(normalized, payload["question_labels"], frames):
            reason_codes.append(SYNTHETIC_PARTITION_REASON)
        if semantic_status == "completed":
            # 主校验先撤回错误类别；仅真实字框证据完整的小型禁区才改为说明区，混合作答大框仍保留。
            reason_codes.extend(review_forbidden_regions(image, page, normalized, payload))
        # 本地核验才能排除的说明牌，在精修时仍可能是未知框；排除后再计算空间冲突，避免留下伪冲突。
        # 不清除模型自报或其他校验产生的原因，仍有重叠的实际作答区须继续纠错。
        _review_region_conflicts(normalized, page, semantic_completed=semantic_status == "completed")
        if semantic_status == "completed" and self.prompt_variant != "v8-layout":
            # 局部横线 ROI 没有四边闭合框，须在吸附之后追加，避免被重新放大到含禁区的整栏。
            fill_labels = [label for label in payload.get("fill_blank_question_labels", [])
                           if label in payload["question_labels"] and label not in payload.get("objective_question_labels", [])]
            local, recovery_reasons = discover_fill_regions(
                image, page, frames, fill_labels,
                observed_fill_labels={label for row in normalized for label in row.get("_fill_parent_labels", [])})
            reason_codes.extend(recovery_reasons)
            _merge_local_fill_regions(normalized, local, page)
        if semantic_status == "completed" and self.prompt_variant == "v8-layout":
            # 末轮重新看原整页，只给框编号和坐标；不把旧类别、题号喂回模型作为自证。
            # 分阶段共享网关内的有界预算，失败保留已可用结果，不按全场缺题推断本页漏区。
            with trace.active():
                coverage = review_template_page_coverage(
                    self.model, payload, image, normalized, deadline=started + 70)
            usage = sum_model_usage(usage, coverage["usage"])
            trace.metadata["request_count"] += coverage["metadata"]["request_count"]
            trace.metadata["page_coverage"] = coverage["metadata"]
            trace.metadata["page_coverage"]["applications"] = apply_page_coverage(
                normalized, coverage, payload, frames, page, content_evidence)
            reason_codes.extend(coverage["metadata"].get("reason_codes", []))
            coverage_targets = [row for row in normalized if any(
                code == "PAGE_COVERAGE_DISCOVERED_REGION" or code.startswith("PAGE_COVERAGE_") and code.endswith("CONFLICT")
                for code in row["reason_codes"])]
            if coverage_targets:
                with trace.active():
                    final_repair = repair_template_candidates(
                        self.model, payload, image, coverage_targets, deadline=started + 82, max_calls=1)
                usage = sum_model_usage(usage, final_repair["usage"])
                trace.metadata["request_count"] += final_repair["metadata"]["request_count"]
                trace.metadata["coverage_repair"] = final_repair["metadata"]
                trace.metadata["coverage_repair"]["applications"] = _apply_repair_batches(
                    normalized, final_repair["batches"], payload, frames, page, content_evidence,
                    barcode_hints=hints, deadline=started + 82)
                reason_codes.extend(final_repair["metadata"].get("reason_codes", []))
            _review_region_conflicts(normalized, page, semantic_completed=True)
        _review_question_association_conflicts(normalized)
        fill_groups = discover_fill_grading_groups(normalized, image, frames, payload) if semantic_status == "completed" else []
        _finalize_candidate_reasons(normalized, request_id)
        settings = getattr(self.model, "settings", None)
        request_count = trace.metadata.get("request_count", int(attempted))
        trace.metadata["request_count"] = request_count
        trace.finish(semantic_status, normalized, reason_codes)
        return {
            "request_id": request_id,
            "schema_version": SCHEMA_VERSION,
            "status": "review_required",
            "coordinate_space": "source_display_normalized",
            "page_no": page["page_no"],
            "source_sha256": page["sha256"],
            "candidates": normalized,
            "fill_grading_groups": fill_groups,
            "reason_codes": reason_codes,
            "semantic_status": semantic_status,
            "semantic_error": semantic_error,
            "model_identity": {field: str(getattr(settings, field, "") or "") for field in (
                "provider_key", "model_name", "model_version",
            )},
            "versions": dict(self.versions),
            "model_usage": describe_model_usage(self.model, usage, request_count=request_count),
        }


def _apply_repair_batches(*args, **kwargs):
    # 保留入口的依赖替换边界；每次调用读取当前校准器，不缓存测试或部署替换前的对象。
    return _merge_repair_batches(*args, identity_aligner=align_identity_fields, **kwargs)
