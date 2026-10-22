"""离线登记答题卡 system 提示词与参考 schema；默认检查，--write 才重建独立产物。"""

import argparse
import base64
import hashlib
import json
import os
import sys
import tempfile
from pathlib import Path

_PROJECT_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MANIFEST = _PROJECT_ROOT / "ai-services" / "prompts" / "template-analysis" / "manifest.v7.json"
_MAX_MANIFEST_BYTES = 64 * 1024
_VARIANTS = ("v6", "v7-text", "v7-evidence", "v8-layout")


def canonical_json(value):
    """与逐请求诊断使用同样的 JSON 编码参数，但此处只摘要固定参考 schema。"""
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


def _reference_payload():
    # 只用于调用实际消息构造函数；不分析图片、不实例化供应商，也不读取真实模板。
    raw = b"synthetic-prompt-manifest-reference"
    return {
        "request_id": "prompt-manifest-reference-v1", "schema_version": "answer-sheet-analysis-v1",
        "page": {"page_no": 1, "width": 1, "height": 1, "media_type": "image/png",
                 "data_base64": base64.b64encode(raw).decode("ascii"), "sha256": hashlib.sha256(raw).hexdigest()},
        "candidates": [], "question_labels": [], "objective_question_labels": [],
        "fill_blank_question_labels": [], "writing_state": "blank",
    }


def build_manifest():
    """从生产消息构造入口取完整 system；不复制 v6 字面量或发起识别调用。"""
    source_root = str(_PROJECT_ROOT / "ai-services")
    if source_root not in sys.path:
        sys.path.insert(0, source_root)
    from grading_agent.template_analysis import _messages
    from grading_agent.template_analysis_coarse import coarse_model_output_schema
    from grading_agent.template_analysis_prompt import prompt_version
    from grading_agent.template_analysis_schema import (
        PROMPT_VERSION,
        model_output_schema,
    )

    variants = {}
    for variant in _VARIANTS:
        messages = _messages(_reference_payload(), variant)
        system = [message["content"] for message in messages if message.get("role") == "system"]
        if len(system) != 1 or not isinstance(system[0], str):
            raise ValueError("PROMPT_SYSTEM_CONTRACT_CHANGED")
        encoded = system[0].encode("utf-8")
        observations = variant in {"v7-evidence", "v8-layout"}
        schema = (coarse_model_output_schema() if variant == "v8-layout"
                  else model_output_schema([], 1, include_observations=observations))
        variants[variant] = {
            "prompt_version": PROMPT_VERSION if variant == "v6" else prompt_version(variant),
            "system_prompt_sha256": hashlib.sha256(encoded).hexdigest(),
            "system_prompt_utf8_bytes": len(encoded),
            "include_observations": observations,
            "reference_model_output_schema_sha256": hashlib.sha256(canonical_json(schema).encode("utf-8")).hexdigest(),
        }
    return {
        "schema_version": 1,
        "generated_by": "python scripts/evals/check_template_analysis_prompt_manifest.py --write",
        "default_prompt_variant": "v7-evidence",
        "system_prompt_encoding": "UTF-8",
        "reference_schema": {
            "scope": "reference_only_not_per_request", "candidate_ids": [], "page_no": 1,
            "canonical_json": {"ensure_ascii": False, "sort_keys": True,
                               "separators": [",", ":"], "allow_nan": False},
            "per_request_digest": "AnalysisTrace.metadata.request_contract.model_output_schema_sha256",
        },
        "variants": variants,
    }


def _reject_constant(_value):
    raise ValueError("PROMPT_MANIFEST_INVALID")


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("PROMPT_MANIFEST_INVALID")
        result[key] = value
    return result


def check_manifest(expected, path=None):
    """返回固定状态码；默认检查不会创建目录、修复文件或回显文件内容。"""
    target = DEFAULT_MANIFEST if path is None else Path(path)
    try:
        with target.open("rb") as handle:
            raw = handle.read(_MAX_MANIFEST_BYTES + 1)
        if len(raw) > _MAX_MANIFEST_BYTES:
            return "PROMPT_MANIFEST_INVALID"
        actual = json.loads(raw.decode("utf-8"), parse_constant=_reject_constant, object_pairs_hook=_unique_object)
        return "PROMPT_MANIFEST_MATCH" if canonical_json(actual) == canonical_json(expected) else "PROMPT_MANIFEST_DRIFT"
    except FileNotFoundError:
        return "PROMPT_MANIFEST_MISSING"
    except (ValueError, UnicodeError, RecursionError):
        return "PROMPT_MANIFEST_INVALID"
    except OSError:
        return "PROMPT_MANIFEST_READ_FAILED"


def write_manifest(expected, path=None):
    """仅显式生成时替换完整产物；先写同目录临时文件，避免留下半份登记。"""
    target = DEFAULT_MANIFEST if path is None else Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=".prompt-manifest-", suffix=".tmp", dir=target.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(expected, handle, ensure_ascii=False, indent=2, allow_nan=False)
            handle.write("\n")
        os.replace(temporary, target)
    finally:
        Path(temporary).unlink(missing_ok=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", action="store_true", help="重建独立 template-analysis/manifest.v7.json")
    arguments = parser.parse_args(argv)
    try:
        expected = build_manifest()
    except (ImportError, ValueError, TypeError, KeyError):
        print("PROMPT_MANIFEST_BUILD_FAILED", file=sys.stderr)
        return 2
    if arguments.write:
        try:
            write_manifest(expected)
        except OSError:
            print("PROMPT_MANIFEST_WRITE_FAILED", file=sys.stderr)
            return 2
        print("PROMPT_MANIFEST_WRITTEN")
        return 0
    status = check_manifest(expected)
    print(status)
    return 0 if status == "PROMPT_MANIFEST_MATCH" else 1


if __name__ == "__main__":
    raise SystemExit(main())
