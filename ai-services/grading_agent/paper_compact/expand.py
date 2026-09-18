"""Expand compact model references into authoritative durable provenance."""

from ..errors import AgentError


def expand_compact_output(output, chunk, chunk_index):
    if not isinstance(output, dict):
        raise AgentError(
            "model_output_invalid", "paper parser output was invalid", status=502
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
                "model_output_invalid", "paper parser output was invalid", status=502
            )
        for item_index, item in enumerate(values, 1):
            if not isinstance(item, dict):
                raise AgentError(
                    "model_output_invalid",
                    "paper parser output was invalid",
                    status=502,
                )
            expanded = dict(item)
            expanded["candidate_id"] = f"{prefix}-{chunk_index:03d}-{item_index:03d}"
            expanded["source_refs"] = _hydrate_refs(item.get("source_refs"), chunk)
            if collection == "solution_candidates":
                steps = item.get("steps", [])
                expanded["steps"] = [
                    {"step_no": index, "content": str(content)}
                    for index, content in enumerate(steps, 1)
                ]
            elif collection == "rubric_candidates":
                points = []
                for point_index, point in enumerate(item.get("points", []), 1):
                    if not isinstance(point, dict):
                        raise AgentError(
                            "model_output_invalid",
                            "rubric point was invalid",
                            status=502,
                        )
                    points.append(
                        {
                            "id": f"rp-{chunk_index:03d}-{item_index:03d}-{point_index:03d}",
                            "description": point.get("description", ""),
                            "score": point.get("score"),
                            "required": point.get("required"),
                            "evidence_requirements": [],
                        }
                    )
                expanded["points"] = points
            result[collection].append(expanded)

    issues = output.get("issues")
    if not isinstance(issues, list):
        raise AgentError(
            "model_output_invalid", "paper parser output was invalid", status=502
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


def _hydrate_refs(aliases, chunk):
    if not isinstance(aliases, list) or not aliases:
        raise AgentError(
            "model_output_invalid", "candidate provenance was not grounded", status=502
        )
    refs = []
    seen = set()
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
