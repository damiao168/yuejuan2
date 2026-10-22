"""重放固定模型响应，仅验收契约和降级边界；不调用供应商、OCR、队列或数据库。"""

import argparse
import copy
import json
import os
import re
import sys
from contextlib import ExitStack, nullcontext
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

_PROJECT_ROOT = Path(__file__).resolve().parents[2]
_MAX_INPUT_BYTES = 16 * 1024 * 1024
_MAX_CASES = 16
_CASE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$")
_HASH = re.compile(r"^[0-9a-f]{64}$")
_VARIANTS = {"v6", "v7-text", "v7-evidence"}
# 报告只回显约定的固定系统原因；模型可填入任意大写码，不能把这些字符串当成可公开正文。
_SAFE_REASONS = frozenset({
    "SEMANTIC_ANALYSIS_FAILED", "SEMANTIC_MODEL_NOT_CONFIGURED", "MODEL_STRUCTURED_OUTPUT_INVALID",
    "MODEL_RESPONSE_NOT_COMPLETED", "MODEL_REPEATED_CANDIDATE", "NO_GEOMETRY_CANDIDATES",
    "MODEL_OBSERVATIONS_INVALID", "MODEL_OBS_OBJECTIVE_QUESTION_TYPE_CONFLICT",
    "MODEL_OBS_IDENTITY_OPTION_CONFLICT", "MODEL_OBS_WARNING_CATEGORY_CONFLICT",
    "MODEL_OBS_BARCODE_EVIDENCE_MISSING", "MODEL_OBS_BARCODE_TYPE_CONFLICT",
    "MODEL_OBS_IDENTITY_QUESTION_CONFLICT", "MODEL_OBS_IDENTITY_LABEL_CONFLICT",
    "MODEL_OBS_IDENTITY_EVIDENCE_MISSING", "MODEL_OBS_OBJECTIVE_PATTERN_MISSING",
    "MODEL_OBS_IDENTITY_STATIC_LABEL_CONFLICT", "MODEL_OBS_ANSWER_STATIC_LABEL_CONFLICT",
    "MODEL_OBS_OBJECTIVE_IDENTITY_CONFLICT", "MODEL_OBS_OBJECTIVE_BARCODE_CONFLICT",
    "MODEL_OBS_ANSWER_IDENTITY_CONFLICT", "MODEL_OBS_ANSWER_BARCODE_CONFLICT",
    "MODEL_OBS_BARCODE_IDENTITY_CONFLICT", "MODEL_OBS_IDENTITY_BARCODE_CONFLICT", "MODEL_OBS_OTHER_CONTENT_CONFLICT",
    "MODEL_OBS_OBJECTIVE_LABELS_MISSING", "MODEL_OBS_OBJECTIVE_STATIC_LABEL_CONFLICT",
    "MODEL_OBS_FILL_QUESTION_TYPE_CONFLICT", "MODEL_OBS_FILL_OPTION_CONFLICT", "MODEL_OBS_QUESTION_TYPE_CONFLICT",
    "MODEL_OBS_ANSWER_LABEL_CONFLICT", "MODEL_OBS_MULTIPLE_QUESTION_LABELS",
    "MODEL_OBS_INSTRUCTION_CONTENT_CONFLICT", "MODEL_OBS_INSTRUCTION_EVIDENCE_MISSING",
    "MODEL_OBS_QUESTION_LABEL_NOT_IN_LIST", "MODEL_OBS_ANCHOR_OUT_OF_PAGE",
    "MODEL_OBS_ANCHOR_OUTSIDE_REGION", "MODEL_OBS_CANDIDATE_GEOMETRY_INVALID",
    "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT", "SEMANTIC_ROLE_IDENTITY_CONFLICT", "SEMANTIC_ASSOCIATION_CONFLICT",
    "SEMANTIC_EVIDENCE_INSUFFICIENT", "SEMANTIC_CONTENT_CONFLICT", "SEMANTIC_REGION_OVERLAP_CONFLICT",
    "SEMANTIC_ANALYSIS_REQUIRED", "SEMANTIC_CHECK_BUDGET_EXCEEDED", "REGION_ROLE_UNCERTAIN",
    "MODEL_DISCOVERED_REGION", "MODEL_LAYOUT_REQUIRES_REVIEW", "MODEL_GEOMETRY_REQUIRES_REVIEW",
    "OBJECTIVE_GROUP_REQUIRES_REVIEW", "IDENTITY_LAYOUT_REQUIRES_REVIEW", "REGION_CONFLICT",
    "NO_PRINTED_FRAME_REQUIRES_REVIEW", "PRINTED_FRAME_VERIFIED", "MODEL_GEOMETRY_DISAGREEMENT",
    "QUESTION_LABEL_NOT_IN_LIST", "MODEL_CAUTION_REQUIRES_REVIEW", "MULTIPLE_REGIONS_FOR_QUESTION",
    "QUESTION_ASSOCIATION_WITHOUT_EVIDENCE", "OBJECTIVE_QUESTION_REQUIRES_GROUP",
    "HANDWRITING_LAYOUT_REQUIRES_REVIEW", "WRITING_STATE_UNKNOWN", "MODEL_REASON_CODES_TRUNCATED",
    "MULTIMODAL_OBSERVATIONS_CONSISTENT", "MULTIMODAL_CONTENT_CONSISTENT", "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED",
    "MODEL_OBS_OBJECTIVE_ITEMS_ROLE_CONFLICT", "MODEL_OBS_OBJECTIVE_ITEMS_MISSING", "MODEL_OBS_OBJECTIVE_ITEMS_INVALID",
    "MODEL_OBS_OBJECTIVE_ITEMS_QUESTION_CONFLICT", "MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT",
    "MODEL_OBS_OBJECTIVE_ITEMS_OPTIONS_CONFLICT", "MODEL_OBS_OBJECTIVE_ITEMS_COVERAGE_CONFLICT",
})


class ReplayInputError(ValueError):
    """只带固定错误码，不能携带输入路径、个人值或供应商原文。"""


class _ReplayModel:
    def __init__(self, response):
        self.response = response
        self.calls = []
        self.settings = SimpleNamespace(provider_key="offline-fixture", model_name="contract-replay", model_version="fixed")

    def session(self, _request_id):
        return nullcontext()

    def request_structured(self, _request_id, messages, schema, _name):
        self.calls.append((copy.deepcopy(messages), copy.deepcopy(schema)))
        return copy.deepcopy(self.response)

    def last_usage(self):
        return {}


def _validate_corpus(corpus):
    if (not isinstance(corpus, dict) or set(corpus) != {"schema_version", "cases"}
            or type(corpus["schema_version"]) is not int or corpus["schema_version"] != 1
            or not isinstance(corpus["cases"], list) or not 1 <= len(corpus["cases"]) <= _MAX_CASES):
        raise ReplayInputError("REPLAY_CORPUS_INVALID")
    seen = set()
    for case in corpus["cases"]:
        if (not isinstance(case, dict) or set(case) != {"case_id", "prompt_variant", "request", "model_output"}
                or not isinstance(case["case_id"], str) or not _CASE_ID.fullmatch(case["case_id"])
                or case["case_id"] in seen or not isinstance(case["prompt_variant"], str)
                or case["prompt_variant"] not in _VARIANTS
                or not isinstance(case["request"], dict) or not isinstance(case["model_output"], dict)):
            raise ReplayInputError("REPLAY_CASE_INVALID")
        seen.add(case["case_id"])


def _contract_metadata(metadata):
    contract = metadata.get("request_contract")
    if not isinstance(contract, dict):
        return None
    if (contract.get("prompt_variant") not in _VARIANTS
            or contract.get("requested_output_mode") != "schema_contract"
            or any(not isinstance(contract.get(key), str) or not _HASH.fullmatch(contract[key])
                   for key in ("system_prompt_sha256", "model_output_schema_sha256"))):
        return None
    return {key: contract[key] for key in ("prompt_variant", "system_prompt_sha256",
                                          "model_output_schema_sha256", "requested_output_mode")}


def replay_corpus(corpus):
    """按顺序处理最多 16 例；请求图片仍走既有 hash/尺寸校验，局部外部证据全部关闭。"""
    _validate_corpus(corpus)
    # 工具从固定仓库位置导入生产后处理，不导入或执行夹具指定的 Python 文件。
    source_root = str(_PROJECT_ROOT / "ai-services")
    if source_root not in sys.path:
        sys.path.insert(0, source_root)
    from grading_agent.errors import AgentError
    from grading_agent.template_analysis import TemplateAnalyzer
    from grading_agent.template_analysis_diagnostics import AnalysisTrace
    from grading_agent.template_analysis_semantics import LocalContentEvidence

    def no_external_evidence(image, page):
        return LocalContentEvidence(image, page, ocr=lambda _image: ([], None), barcode=lambda _image: None)

    cases = []
    with ExitStack() as scope:
        # 继承环境即使开启原文采集，本工具也不写真实图像/供应商响应诊断文件。
        scope.enter_context(patch.dict(os.environ, {"EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR": "",
                                                    "EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT": "false"}))
        # 调用方可能已有 INFO 日志配置；请求 ID 也来自夹具，只取内存摘要而不打印生产诊断日志。
        scope.enter_context(patch("grading_agent.template_analysis_diagnostics._LOGGER.info"))
        scope.enter_context(patch("grading_agent.template_analysis.discover_fill_regions", return_value=([], [])))
        scope.enter_context(patch("grading_agent.template_analysis.review_forbidden_regions", return_value=[]))
        original_finish = AnalysisTrace.finish
        request_contracts = []

        def finish(trace, *args, **kwargs):
            original_finish(trace, *args, **kwargs)
            request_contracts.append(_contract_metadata(trace.metadata))

        scope.enter_context(patch.object(AnalysisTrace, "finish", finish))
        for case in corpus["cases"]:
            request_contracts.clear()
            model = _ReplayModel(case["model_output"])
            summary = {"case_id": case["case_id"], "prompt_variant": case["prompt_variant"],
                       "semantic_status": "failed", "reason_codes": [], "candidate_count": 0,
                       "needs_review_count": 0, "supported_count": 0, "model_calls": 0,
                       "unreported_reason_count": 0, "request_contract": None, "error_code": None}
            try:
                result = TemplateAnalyzer(model, no_external_evidence,
                                          prompt_variant=case["prompt_variant"]).analyze(case["request"])
                candidates = result["candidates"]
                reasons = set(result["reason_codes"])
                for candidate in candidates:
                    reasons.update(candidate["reason_codes"])
                summary.update(semantic_status=result["semantic_status"], candidate_count=len(candidates),
                               needs_review_count=sum(row.get("semantic_check") == "needs_review" for row in candidates),
                               supported_count=sum(row.get("semantic_check") == "supported" for row in candidates),
                               reason_codes=sorted(reasons & _SAFE_REASONS),
                               unreported_reason_count=len(reasons - _SAFE_REASONS))
            except AgentError as error:
                summary["error_code"] = "REPLAY_REQUEST_REJECTED" if error.code == "invalid_request" else "REPLAY_ANALYSIS_FAILED"
            except (ValueError, TypeError, KeyError, OSError, RuntimeError):
                summary["error_code"] = "REPLAY_PROCESSING_FAILED"
            summary["model_calls"] = len(model.calls)
            summary["request_contract"] = request_contracts[-1] if request_contracts else None
            cases.append(summary)
    return {"schema_version": 1, "contract_replay_only": True, "supplier_called": False,
            "external_ocr_called": False, "database_written": False, "raw_content_emitted": False,
            "cases": cases, "counts": {"cases": len(cases), "completed": sum(
                case["semantic_status"] == "completed" for case in cases)},
            "quality_claim": "no_model_accuracy_or_geometry_quality_claim"}


def _reject_nonfinite(_value):
    raise ReplayInputError("REPLAY_JSON_NONFINITE")


def load_corpus(path):
    if path.stat().st_size > _MAX_INPUT_BYTES:
        raise ReplayInputError("REPLAY_INPUT_TOO_LARGE")
    try:
        # 文件可能在 stat 后被改写；读取量也设上限，不能只检查最初的文件大小。
        with path.open("rb") as handle:
            raw = handle.read(_MAX_INPUT_BYTES + 1)
        if len(raw) > _MAX_INPUT_BYTES:
            raise ReplayInputError("REPLAY_INPUT_TOO_LARGE")
        return json.loads(raw, parse_constant=_reject_nonfinite)
    except (json.JSONDecodeError, UnicodeError, RecursionError) as error:
        raise ReplayInputError("REPLAY_JSON_INVALID") from error


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("corpus", type=Path)
    parser.add_argument("--output", type=Path)
    arguments = parser.parse_args()
    try:
        # resolve 覆盖相对路径与符号别名，samefile 再拦截硬链；报告不能截断原始重放语料。
        if arguments.output and (arguments.output.resolve() == arguments.corpus.resolve()
                                 or (arguments.output.exists() and arguments.output.samefile(arguments.corpus))):
            raise ReplayInputError("REPLAY_OUTPUT_ALIASES_INPUT")
        result = replay_corpus(load_corpus(arguments.corpus))
        text = json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False) + "\n"
        if arguments.output:
            arguments.output.write_text(text, encoding="utf-8")
        else:
            print(text, end="")
    except ReplayInputError as error:
        print(json.dumps({"error_code": str(error), "contract_replay_only": True}), file=sys.stderr)
        return 1
    except (OSError, ValueError, TypeError, KeyError, RecursionError):
        print('{"error_code":"REPLAY_INPUT_OR_OUTPUT_FAILED","contract_replay_only":true}', file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
