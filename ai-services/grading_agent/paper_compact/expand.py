"""Expand compact model references into authoritative durable provenance."""

from ..errors import AgentError

PARSER_OUTPUT_INVALID = "paper parser output was invalid"


def expand_compact_output(output, chunk, chunk_index):
    if not isinstance(output, dict):
        raise AgentError(
            "model_output_invalid", PARSER_OUTPUT_INVALID, status=502
        )
    result = {
        "documents": output.get("documents", []),
        "question_candidates": [],
        "answer_candidates": [],
        "solution_candidates": [],
        "rubric_candidates": [],
        "issues": [],
    }
    definitions = (
        ("question_candidates", "q"),
        ("answer_candidates", "a"),
        ("solution_candidates", "s"),
        ("rubric_candidates", "r"),
    )
    for collection, prefix in definitions:
        values = output.get(collection)
        if not isinstance(values, list):
            raise AgentError(
                "model_output_invalid", PARSER_OUTPUT_INVALID, status=502
            )
        for item_index, item in enumerate(values, 1):
            result[collection].append(_expand_candidate(item, collection, prefix, chunk, chunk_index, item_index))

    issues = output.get("issues")
    if not isinstance(issues, list):
        raise AgentError(
            "model_output_invalid", PARSER_OUTPUT_INVALID, status=502
        )
    for issue in issues:
        if not isinstance(issue, dict):
            raise AgentError(
                "model_output_invalid", "paper parser issue was invalid", status=502
            )
        expanded = dict(issue)
        aliases = issue.get("source_refs", [])
        expanded["source_refs"] = _hydrate_refs(aliases, chunk) if aliases else []
        result["issues"].append(expanded)
    return result


def _expand_candidate(item, collection, prefix, chunk, chunk_index, item_index):
    if not isinstance(item, dict):
        raise AgentError("model_output_invalid", PARSER_OUTPUT_INVALID, status=502)
    expanded = dict(item)
    # 编号由分片位置生成；模型短引用只负责定位，不具备持久化候选身份的权限。
    expanded["candidate_id"] = f"{prefix}-{chunk_index:03d}-{item_index:03d}"
    expanded["source_refs"] = _hydrate_refs(item.get("source_refs"), chunk)
    if collection == "solution_candidates":
        expanded["steps"] = [
            {"step_no": index, "content": str(content)}
            for index, content in enumerate(item.get("steps", []), 1)
        ]
    elif collection == "rubric_candidates":
        expanded["points"] = _expand_rubric_points(item.get("points", []), chunk_index, item_index)
    return expanded


def _expand_rubric_points(points, chunk_index, item_index):
    result = []
    for point_index, point in enumerate(points, 1):
        if not isinstance(point, dict):
            raise AgentError("model_output_invalid", "rubric point was invalid", status=502)
        result.append({
            "id": f"rp-{chunk_index:03d}-{item_index:03d}-{point_index:03d}",
            "description": point.get("description", ""),
            "score": point.get("score"),
            "required": point.get("required"),
            "evidence_requirements": [],
        })
    return result


def _hydrate_refs(aliases, chunk):
    if not isinstance(aliases, list) or not aliases:
        raise AgentError(
            "model_output_invalid", "candidate provenance was not grounded", status=502
        )
    refs = []
    seen = set()
    # rNNN 只在当前分片内有效；不存在或重复的短引用直接拒绝，不能猜测相邻分片来源。
    for alias in aliases:
        if not isinstance(alias, str) or alias in seen or alias not in chunk.references:
            raise AgentError(
                "model_output_invalid",
                "candidate provenance was not grounded",
                status=502,
            )
        seen.add(alias)
        refs.append(dict(chunk.references[alias]))
    return refs
