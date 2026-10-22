"""答题卡区域离线评测；只读取脱敏标注/预测，不调用模型或处理学生填写值。"""

import argparse
from collections import Counter, defaultdict
import json
import math
from pathlib import Path


def _box(region):
    value = region["bbox"]
    values = [value[key] for key in ("x", "y", "width", "height")]
    if any(isinstance(item, bool) or not isinstance(item, (int, float)) or not math.isfinite(item) for item in values):
        raise ValueError("invalid normalized bbox")
    x, y, w, h = values
    if min(x, y) < 0 or min(w, h) <= 0 or x + w > 1 + 1e-9 or y + h > 1 + 1e-9:
        raise ValueError("invalid normalized bbox")
    return x, y, w, h


def _overlap(first, second):
    ax, ay, aw, ah = _box(first)
    bx, by, bw, bh = _box(second)
    intersection = max(0, min(ax + aw, bx + bw) - max(ax, bx)) * max(0, min(ay + ah, by + bh) - max(ay, by))
    return intersection, aw * ah, bw * bh


def _question(region):
    return region.get("question_id") or region.get("printed_question_label")


def _category(region):
    # 身份区域的总类别相同不代表字段识别正确；姓名误成条码也必须计入分类错误。
    label = region.get("identity_label")
    return f"identity:{label}" if region["role"] == "identity" and isinstance(label, str) and label else region["role"]


def _metrics(labels):
    metrics = {}
    for label, result in sorted(labels.items()):
        tp, fp, fn = result["tp"], result["fp"], result["fn"]
        precision = tp / (tp + fp) if tp + fp else None
        recall = tp / (tp + fn) if tp + fn else None
        metrics[label] = {"tp": tp, "fp": fp, "fn": fn, "precision": precision, "recall": recall,
                          "f1": 2 * tp / (2 * tp + fp + fn) if 2 * tp + fp + fn else None}
    return metrics


def evaluate(corpus, iou_threshold=.5):
    """先按位置一对一匹配，再统计类别与题号；错类别不能被当成正确检测。"""
    if not 0 < iou_threshold <= 1:
        raise ValueError("iou threshold must be in (0, 1]")
    families = defaultdict(set)
    for document in corpus["documents"]:
        families[document["template_family"]].add(document["split"])
    if any(len(splits) != 1 for splits in families.values()):
        raise ValueError("template family leaked across dataset splits")
    labels, confusion = defaultdict(Counter), Counter()
    categories, category_confusion = defaultdict(Counter), Counter()
    counts = Counter()
    geometry = {"iou": [], "missed_writable_fraction": [], "outside_truth_fraction": []}
    for document in corpus["documents"]:
        for page in document["pages"]:
            truth, predictions = page["ground_truth"], page["predictions"]
            for region in truth + predictions:
                _box(region)
                if not isinstance(region.get("role"), str) or not region["role"]:
                    raise ValueError("region role required")
            counts.update(truth_regions=len(truth), predicted_regions=len(predictions), pages=1)
            # 使用固定的最大 IoU 贪心匹配；阈值会写入报告，不能把它当作生产自动确认阈值。
            pairs = []
            for ti, expected in enumerate(truth):
                for pi, actual in enumerate(predictions):
                    intersection, expected_area, actual_area = _overlap(expected, actual)
                    iou = intersection / (expected_area + actual_area - intersection)
                    if iou >= iou_threshold:
                        pairs.append((iou, ti, pi, intersection, expected_area, actual_area))
            seen_truth, seen_predictions = set(), set()
            for iou, ti, pi, intersection, expected_area, actual_area in sorted(pairs, reverse=True):
                if ti in seen_truth or pi in seen_predictions:
                    continue
                seen_truth.add(ti)
                seen_predictions.add(pi)
                expected, actual = truth[ti], predictions[pi]
                confusion[(expected["role"], actual["role"])] += 1
                geometry["iou"].append(iou)
                geometry["missed_writable_fraction"].append(1 - intersection / expected_area)
                geometry["outside_truth_fraction"].append(1 - intersection / actual_area)
                same_role = expected["role"] == actual["role"]
                expected_category, actual_category = _category(expected), _category(actual)
                same_category = expected_category == actual_category
                category_confusion[(expected_category, actual_category)] += 1
                if same_category:
                    categories[expected_category]["tp"] += 1
                else:
                    categories[expected_category]["fn"] += 1
                    categories[actual_category]["fp"] += 1
                if same_role:
                    labels[expected["role"]]["tp"] += 1
                else:
                    labels[expected["role"]]["fn"] += 1
                    labels[actual["role"]]["fp"] += 1
                if _question(expected) is not None:
                    counts["question_matches"] += int(same_role and _question(actual) == _question(expected))
                if actual.get("semantic_check") == "supported":
                    counts["supported_predictions"] += 1
                    counts["wrong_supported_predictions"] += int(not same_category or _question(actual) != _question(expected))
                if expected["role"] == "instruction" and actual["role"] in {"answer", "objective_group", "identity"}:
                    counts["excluded_area_misclassified"] += 1
            for ti, expected in enumerate(truth):
                if _question(expected) is not None:
                    counts["question_regions"] += 1
                if ti not in seen_truth:
                    labels[expected["role"]]["fn"] += 1
                    categories[_category(expected)]["fn"] += 1
                    counts["missed_regions"] += 1
            for pi, actual in enumerate(predictions):
                if pi not in seen_predictions:
                    labels[actual["role"]]["fp"] += 1
                    categories[_category(actual)]["fp"] += 1
                    if actual.get("semantic_check") == "supported":
                        counts["supported_predictions"] += 1
                        counts["wrong_supported_predictions"] += 1
    return {"schema_version": 1, "matching": "greedy_max_iou_before_role", "iou_threshold": iou_threshold,
            "counts": dict(counts), "per_role": _metrics(labels), "per_category": _metrics(categories),
            "confusion": [{"expected": expected, "predicted": actual, "count": count}
                          for (expected, actual), count in sorted(confusion.items())],
            "category_confusion": [{"expected": expected, "predicted": actual, "count": count}
                                   for (expected, actual), count in sorted(category_confusion.items())],
            "matched_geometry_mean": {key: sum(values) / len(values) if values else None for key, values in geometry.items()},
            "question_mapping_recall": counts["question_matches"] / counts["question_regions"] if counts["question_regions"] else None,
            "note": "几何均值仅统计匹配区域，须同时查看漏检；模型置信未校准，本报告不能替代真实样本验收。"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("corpus", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--iou", type=float, default=.5)
    arguments = parser.parse_args()
    result = evaluate(json.loads(arguments.corpus.read_text(encoding="utf-8")), arguments.iou)
    data = json.dumps(result, ensure_ascii=False, indent=2)
    if arguments.output:
        arguments.output.write_text(data + "\n", encoding="utf-8")
    else:
        print(data)


if __name__ == "__main__":
    main()
