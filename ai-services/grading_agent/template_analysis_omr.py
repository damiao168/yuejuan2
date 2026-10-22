"""多模态先确定选择题用途，OMR 实测格子，再用实际题号锚点确定唯一排布。

正式题目清单只用于核对，不能生成本页未观察到的题号或缺失选项。
OCR 只补读每组左侧静态题号窄带；无法唯一对应时保留原行，继续既有模型修复。
"""

import copy
import csv
import io
import os
import re
import shutil
import subprocess
import time
from itertools import pairwise, product
from statistics import median

import cv2
import numpy as np

from .answer_sheet_vision import detect_printed_frames
from .choice_grid_detection import detect_option_groups
from .template_analysis_frame_alignment import complete_coarse_objective_frame
from .template_analysis_objective_items import review_objective_items
from .template_analysis_observations import review_model_observations
from .template_analysis_option_alignment import (
    _anchors,
    _bounds,
    _box,
    _choice_content,
    _contains,
    _feature,
    _frame_enclosure,
    _inside_token,
    _measured_item_bounds,
    _pixel_edge,
    _positive_overlap,
)
from .template_analysis_registration import transform_registration_bbox

OMR_LAYOUT_VERSION = "coarse-semantic-measured-omr-layout-v3"
_KEYS = ("x", "y", "width", "height")
_SECONDS = 4.0


def populate_objective_items(rows, image, payload, *, tokens=(), frames=(), deadline=None, label_reader=None,
                             source_image=None, source_to_analysis=None):
    """原子补齐每个粗选择区；应在 normalize 清除本页模型观察字段之前调用。"""
    result = {"version": OMR_LAYOUT_VERSION, "completed_indices": [], "matched_items": 0,
              "verified_anchors": 0, "label_read_count": 0, "merged_groups": 0, "reason_codes": []}
    if image is None or image.ndim != 3 or image.shape[2] != 3:
        return result
    # 校正副本会重采样六至十像素的小括号；OMR 可保留原始像素测量，再用同一定位矩阵映射。
    # 语义仍基于校正图和模型观察，避免把纸张倾斜变成模型反复读格子的负担。
    source_matrix = None
    if source_image is not None and source_to_analysis is not None:
        matrix = np.asarray(source_to_analysis, dtype=np.float64)
        if (source_image.shape != image.shape or matrix.shape != (3, 3) or not np.isfinite(matrix).all()
                or abs(float(np.linalg.det(matrix))) < 1e-10):
            return result
        source_matrix = matrix
    stop = min(deadline if deadline is not None else float("inf"), time.monotonic() + _SECONDS)
    reader = label_reader or _read_number_strip_tokens
    completion_data = {}
    for index, original in enumerate(rows):
        if (original.get("role") != "objective_group" or original.get("objective_items")
                or original.get("_coarse_layout") is not True):
            continue
        row = copy.deepcopy(original)
        observed = row.get("observed_question_labels")
        official = set(payload.get("objective_question_labels", []))
        if (not isinstance(observed, list) or not 1 <= len(observed) <= 256
                or len(set(observed)) != len(observed) or not set(observed) <= official
                or any(not isinstance(label, str) or not re.fullmatch(r"\d{1,3}", label) for label in observed)):
            continue
        review_model_observations(row, payload)
        if row.get("semantic_check") == "needs_review":
            continue
        complete_coarse_objective_frame(row, frames, payload.get("page", {}), rows)
        region = _box(row.get("proposed_bbox"))
        if region is None:
            continue
        measurement_image, measurement_region = image, region
        measurement_tokens = tokens
        if source_matrix is not None:
            measurement_image = source_image
            inverse = np.linalg.inv(source_matrix)
            measurement_region = _box(transform_registration_bbox(dict(zip(_KEYS, region)), inverse, image.shape[1], image.shape[0]))
            measurement_tokens = [{**token, **transform_registration_bbox(token, inverse, image.shape[1], image.shape[0])}
                                  for token in tokens if _box(token) is not None]
        if measurement_region is None:
            continue
        measurement_frames = (detect_printed_frames(measurement_image, payload.get("page", {}).get("page_no", 1))
                              if source_matrix is not None else frames)
        section_boxes = _measurement_sections(measurement_region, measurement_frames)
        if section_boxes:
            previous_region = region
            measurement_region = _bounds(section_boxes)
            row["proposed_bbox"] = dict(zip(_KEYS, measurement_region))
            if source_matrix is not None:
                row["proposed_bbox"] = transform_registration_bbox(row["proposed_bbox"], source_matrix, image.shape[1], image.shape[0])
            region = _box(row["proposed_bbox"])
            # 原粗区内的留白屏障只阻止跨区合并，不抹掉该区独立实测的选项。
            # 印刷框精修如果新增覆盖其他用途，即使之前已有部分交叠，也必须保留原候选待修复。
            if any(other is not original and other.get("role") != "objective_group"
                   and (barrier := _box(other.get("proposed_bbox"))) is not None
                   and _overlap_area(region, barrier) > _overlap_area(previous_region, barrier) + 1e-12
                   for other in rows):
                continue
        measured = [groups for groups in _measure(measurement_image, measurement_region, None, sections=section_boxes)
                    if len(groups) >= len(observed)]
        if len(measured) != 1:
            continue
        groups = measured[0]
        inside = [token for token in measurement_tokens if _inside_token(token, measurement_region)]
        anchors = _anchors(inside, groups, official, "horizontal")
        # 用途仍由多模态负责；OCR 标题缺失不能否定已通过观察矛盾检查的选择区。
        # 独立标题/字母若读到则保留为额外证据，真正身份、条码、禁区反证仍由后续内容层检查。
        independent_choice = _choice_content(inside, groups)
        verified_labels = list(dict.fromkeys([*observed, *(label for label, _index, _box_value in anchors)]))
        assignment = _unique_assignment(groups, verified_labels, anchors) if len(verified_labels) == len(groups) else None
        if assignment is None and result["label_read_count"] < 2 and time.monotonic() < stop:
            montage, windows, stride = _number_montage(measurement_image, groups, measurement_region)
            if montage is not None:
                result["label_read_count"] += 1
                try:
                    read, _reason = reader(montage, timeout=max(.01, stop - time.monotonic()))
                except (OSError, ValueError, subprocess.TimeoutExpired, cv2.error):
                    read = []
                mapped = _map_number_tokens(read, windows, stride, image.shape)
                anchors = _anchors([*inside, *mapped], groups, official, "horizontal")
                # 首轮模型可以漏读题号；只追加在该实测组选项左侧独立读到的题号，不从正式白名单补齐。
                verified_labels = list(dict.fromkeys([*observed, *(label for label, _index, _box_value in anchors)]))
                assignment = _unique_assignment(groups, verified_labels, anchors) if len(verified_labels) == len(groups) else None
        if assignment is None:
            continue
        items = []
        protected = [_box(other["proposed_bbox"]) for other in rows if other is not original
                     and other.get("role") != "objective_group" and _box(other.get("proposed_bbox")) is not None]
        if source_matrix is not None:
            protected = [mapped for box in protected if (mapped := _box(transform_registration_bbox(
                dict(zip(_KEYS, box)), inverse, image.shape[1], image.shape[0]))) is not None]
        item_bounds = _measured_item_bounds(measurement_image, groups, anchors, measurement_region, "horizontal",
                                            protected=protected)
        for group_index, group in enumerate(groups):
            items.append({"printed_question_label": assignment[group_index],
                          "bbox": dict(zip(_KEYS, item_bounds[group_index])),
                          "options": [{"label": letter, "bbox": dict(zip(_KEYS, box))}
                                      for letter, box in zip("ABCDEFGHIJKL", group)]})
        if source_matrix is not None:
            for item in items:
                item["bbox"] = transform_registration_bbox(item["bbox"], source_matrix, image.shape[1], image.shape[0])
                for option in item["options"]:
                    option["bbox"] = transform_registration_bbox(option["bbox"], source_matrix, image.shape[1], image.shape[0])
        if any(not _contains(region, _box(item["bbox"])) for item in items):
            continue
        # 留白可保留为待解的区域冲突；实测题号和选项组成的单题范围碰到其他用途时不能自动验收。
        if any(other is not original and other.get("role") != "objective_group"
               and (barrier := _box(other.get("proposed_bbox"))) is not None
               and any(_positive_overlap(_box(item["bbox"]), barrier) for item in items)
               for other in rows):
            continue
        row["objective_items"] = sorted(items, key=lambda item: int(item["printed_question_label"]))
        row["observed_question_labels"] = verified_labels
        if review_objective_items(row, payload):
            continue
        row["reason_codes"] = list(dict.fromkeys([*row.get("reason_codes", []), "LOCAL_OBJECTIVE_OMR_LAYOUT_VERIFIED"]))
        if set(verified_labels) - set(observed):
            row["reason_codes"].append("LOCAL_OBJECTIVE_NUMBER_LABELS_COMPLETED")
        if source_matrix is not None:
            row["reason_codes"].append("LOCAL_OBJECTIVE_ORIGINAL_PIXELS_MAPPED")
        if independent_choice:
            row["reason_codes"].append("LOCAL_OBJECTIVE_STATIC_CHOICE_CONTENT_VERIFIED")
        reference = f"local-omr:{OMR_LAYOUT_VERSION}:{payload.get('page', {}).get('page_no', 1)}"
        row["evidence_refs"] = list(dict.fromkeys([*row.get("evidence_refs", []), reference]))
        original.update(row)
        result["completed_indices"].append(index)
        result["matched_items"] += len(items)
        result["verified_anchors"] += len({(label, group) for label, group, _box_value in anchors})
        completion_data[index] = {"groups": groups, "measurement_bbox": measurement_region}
    if result["completed_indices"]:
        result["reason_codes"].append("LOCAL_OBJECTIVE_OMR_LAYOUT_VERIFIED")
    if len(completion_data) >= 2:
        result["merged_groups"] = _merge_completed_regions(rows, completion_data, image, payload,
                                                           frames, source_image, source_matrix)
        if result["merged_groups"]:
            result["completed_indices"] = [index for index, row in enumerate(rows)
                                            if "LOCAL_OBJECTIVE_OMR_LAYOUT_VERIFIED" in row.get("reason_codes", [])]
            result["reason_codes"].append("LOCAL_OBJECTIVE_OMR_CONTINUOUS_GROUPS_MERGED")
    return result


def _overlap_area(first, second):
    return (max(0, min(first[0] + first[2], second[0] + second[2]) - max(first[0], second[0]))
            * max(0, min(first[1] + first[3], second[1] + second[3]) - max(first[1], second[1])))


def _merge_completed_regions(rows, completed, image, payload, frames, source_image, source_matrix):
    indices = list(completed)
    measured_image = source_image if source_matrix is not None else image
    height, width = measured_image.shape[:2]
    if source_matrix is not None:
        frames = detect_printed_frames(measured_image, payload.get("page", {}).get("page_no", 1))
    groups = [group for data in completed.values() for group in data["groups"]]
    region = _bounds([data["measurement_bbox"] for data in completed.values()])
    # 仅放宽一个印刷线像素，不扩到相邻用途；外框必须真实连续，不能用大区并集填平空隙。
    region = (max(0, region[0] - 2 / width), max(0, region[1] - 2 / height),
              min(1, region[0] + region[2] + 2 / width) - max(0, region[0] - 2 / width),
              min(1, region[1] + region[3] + 2 / height) - max(0, region[1] - 2 / height))
    enclosure = _frame_enclosure(groups, frames, region, width, height)
    if enclosure is None:
        return 0
    final_box = dict(zip(_KEYS, enclosure))
    if source_matrix is not None:
        final_box = transform_registration_bbox(final_box, source_matrix, width, height)
    # 旧身份框、填空框、说明和未知用途都作为屏障；不能为一大框吞掉其他用途。
    if any(index not in completed and (box := _box(row.get("proposed_bbox"))) is not None
           and _positive_overlap(_box(final_box), box) for index, row in enumerate(rows)):
        return 0
    merged = copy.deepcopy(rows[indices[0]])
    labels = [label for index in indices for label in rows[index]["observed_question_labels"]]
    if len(set(labels)) != len(labels):
        return 0
    merged.update(proposed_bbox=final_box, observed_question_labels=labels,
                  objective_items=sorted([item for index in indices for item in rows[index]["objective_items"]],
                                         key=lambda item: int(item["printed_question_label"])))
    merged["evidence_refs"] = list(dict.fromkeys(ref for index in indices for ref in rows[index].get("evidence_refs", [])))
    merged["reason_codes"] = list(dict.fromkeys([*(code for index in indices for code in rows[index].get("reason_codes", [])),
                                               "LOCAL_OBJECTIVE_OMR_CONTINUOUS_GROUPS_MERGED"]))
    if review_objective_items(merged, payload):
        return 0
    rows[:] = [merged if index == indices[0] else row for index, row in enumerate(rows)
               if index == indices[0] or index not in completed]
    return len(indices)


def _measurement_sections(region, frames):
    selected = []
    for frame in frames:
        box = _box(frame.get("bbox"))
        if box is None:
            continue
        horizontal = max(.003, region[2] * .08)
        overlap = max(0, min(region[1] + region[3], box[1] + box[3]) - max(region[1], box[1]))
        if (abs(box[0] - region[0]) <= horizontal and abs(box[0] + box[2] - region[0] - region[2]) <= horizontal
                and overlap >= box[3] * .75 and box[3] <= region[3] * 1.15):
            selected.append(box)
    selected.sort(key=lambda box: box[1])
    if not selected:
        return []
    # 同一用途可跨共边分隔框，不能跨填空或身份区的大空隙来扩大模型粗框。
    for first, second in pairwise(selected):
        if abs(first[1] + first[3] - second[1]) > .003:
            return []
    return selected


def _measure(image, region, expected, *, sections=()):
    height, width = image.shape[:2]
    proposals = []
    for count in (4, 5, 3, 2, 6, 7, 8, 9, 10, 11, 12):
        windows = sections or [region]
        groups = []
        for window in windows:
            sx, sy = _pixel_edge(window[0] * width), _pixel_edge(window[1] * height)
            ex = _pixel_edge((window[0] + window[2]) * width, upper=True)
            ey = _pixel_edge((window[1] + window[3]) * height, upper=True)
            # 实际共边分隔框各自计算格尺度；填黑格偏高不能因另一段更细的括号被整行丢弃。
            measured, _ = detect_option_groups(image[sy:ey, sx:ex], count, "horizontal")
            groups.extend([[((cell[0] + sx) / width, (cell[1] + sy) / height,
                             cell[2] / width, cell[3] / height) for cell in group] for group in measured])
        if groups and (expected is None or len(groups) == expected):
            proposals.append(groups)
    return proposals


def _sections(groups):
    scale = median(_feature(group, "horizontal")[1][1] for group in groups)
    ordered = sorted(range(len(groups)), key=lambda index: _feature(groups[index], "horizontal")[0][1])
    rows = []
    for index in ordered:
        y = _feature(groups[index], "horizontal")[0][1]
        if not rows or abs(y - median(_feature(groups[item], "horizontal")[0][1] for item in rows[-1])) > scale * .8:
            rows.append([])
        rows[-1].append(index)
    centers = [median(_feature(groups[index], "horizontal")[0][1] for index in row) for row in rows]
    gaps = [second - first for first, second in pairwise(centers)]
    typical = median(sorted(gaps)[:max(1, (len(gaps) + 1) // 2)]) if gaps else scale
    sections = [[]]
    for index, row in enumerate(rows):
        if index and gaps[index - 1] > max(typical * 1.9, scale * 3):
            sections.append([])
        sections[-1].extend(row)
    return sections


def _reading_orders(section, groups):
    scale_x = median(_feature(groups[index], "horizontal")[1][0] for index in section)
    columns = []
    for index in sorted(section, key=lambda index: _feature(groups[index], "horizontal")[0][0]):
        x = _feature(groups[index], "horizontal")[0][0]
        if not columns or abs(x - median(_feature(groups[item], "horizontal")[0][0] for item in columns[-1])) > scale_x * .8:
            columns.append([])
        columns[-1].append(index)
    column_order = tuple(index for column in columns for index in sorted(column, key=lambda index: _feature(groups[index], "horizontal")[0][1]))
    # 同一实测横行的亚像素 y 差异不能改变左右阅读顺序。
    rows = []
    scale_y = median(_feature(groups[index], "horizontal")[1][1] for index in section)
    for index in section:
        y = _feature(groups[index], "horizontal")[0][1]
        if not rows or abs(y - median(_feature(groups[item], "horizontal")[0][1] for item in rows[-1])) > scale_y * .8:
            rows.append([])
        rows[-1].append(index)
    row_order = tuple(index for row in rows for index in sorted(row, key=lambda index: _feature(groups[index], "horizontal")[0][0]))
    return {row_order, column_order}


def _unique_assignment(groups, observed, anchors):
    if not anchors:
        return None
    by_group, by_label = {}, {}
    for label, index, _box_value in anchors:
        if label not in observed or index in by_group and by_group[index] != label or label in by_label and by_label[label] != index:
            return None
        by_group[index], by_label[label] = label, index
    if len(by_group) == len(groups):
        return by_group if set(by_group.values()) == set(observed) else None
    sections = _sections(groups)
    labels = sorted(observed, key=int)
    choices, start = [], 0
    for section in sections:
        observed_part = labels[start:start + len(section)]
        start += len(section)
        local_anchors = {index: by_group[index] for index in section if index in by_group}
        # 每个实测分段独立消歧，不能用上一段的题号推定另一段从何处开始。
        if len(local_anchors) < min(2, len(section)):
            return None
        options = []
        for order in _reading_orders(section, groups):
            proposal = dict(zip(order, observed_part, strict=True))
            if all(proposal[index] == label for index, label in local_anchors.items()):
                options.append(proposal)
        if len(options) != 1:
            return None
        choices.append(options)
    assignments = [{index: label for choice in combination for index, label in choice.items()} for combination in product(*choices)]
    return assignments[0] if len(assignments) == 1 else None


def _number_montage(image, groups, region):
    height, width = image.shape[:2]
    windows, strips = [], []
    for group in groups:
        point, size = _feature(group, "horizontal")
        right = _pixel_edge(group[0][0] * width) - 2
        left = max(_pixel_edge(region[0] * width), round(right - size[1] * height * 5))
        top = max(_pixel_edge(region[1] * height), round((point[1] - size[1] * .95) * height))
        bottom = min(_pixel_edge((region[1] + region[3]) * height, upper=True), round((point[1] + size[1] * .95) * height))
        if right <= left or bottom <= top:
            return None, [], 0
        crop = image[top:bottom, left:right]
        gray = cv2.cvtColor(crop, cv2.COLOR_RGB2GRAY)
        gray[crop.max(axis=2).astype(int) - crop.min(axis=2) > 75] = 255
        strips.append(gray)
        windows.append((left, top, right, bottom))
    stride = max(strip.shape[0] for strip in strips) + 12
    montage = np.full((stride * len(strips), max(strip.shape[1] for strip in strips) + 16), 255, np.uint8)
    for index, strip in enumerate(strips):
        montage[index * stride + 6:index * stride + 6 + strip.shape[0], 8:8 + strip.shape[1]] = strip
    return montage, windows, stride


def _map_number_tokens(tokens, windows, stride, shape):
    output = []
    height, width = shape[:2]
    for token in tokens:
        try:
            center = token["y"] + token["height"] / 2
            index = int(center // stride)
            left, top, right, bottom = windows[index]
            x, y = left + token["x"] - 8, top + token["y"] - index * stride - 6
            if (index < 0 or x < left or y < top or x + token["width"] > right or y + token["height"] > bottom
                    or not re.fullmatch(r"\d{1,3}", str(token["text"]))):
                continue
            output.append({"text": token["text"], "x": x / width, "y": y / height,
                           "width": token["width"] / width, "height": token["height"] / height})
        except (IndexError, KeyError, TypeError, ValueError):
            continue
    return output


def _read_number_strip_tokens(image, *, timeout):
    executable = shutil.which("tesseract")
    if executable is None:
        return [], "LOCAL_OBJECTIVE_NUMBER_OCR_UNAVAILABLE"
    deadline, output = time.monotonic() + min(timeout, _SECONDS), []
    for scale in (3, 4):
        remaining = deadline - time.monotonic()
        if remaining <= .01:
            break
        resized = cv2.resize(image, None, fx=scale, fy=scale, interpolation=cv2.INTER_CUBIC)
        data = cv2.imencode(".png", resized)[1].tobytes()
        process = subprocess.run([executable, "stdin", "stdout", "-l", "eng", "--psm", "6", "-c",
                                  "tessedit_char_whitelist=0123456789", "tsv"], input=data, capture_output=True,
                                 timeout=remaining, check=False, env={**os.environ, "OMP_THREAD_LIMIT": "1"})
        if process.returncode or len(process.stdout) > 1024 * 1024:
            continue
        reader = csv.DictReader(io.StringIO(process.stdout.decode("utf8", errors="replace")), delimiter="\t", quoting=csv.QUOTE_NONE)
        for row in reader:
            try:
                text, confidence = row["text"].strip(), float(row["conf"])
                if row["level"] != "5" or confidence < 70 or not re.fullmatch(r"\d{1,3}", text):
                    continue
                output.append({"text": text, **{key: int(row[column]) / scale for key, column in
                              (("x", "left"), ("y", "top"), ("width", "width"), ("height", "height"))}})
            except (KeyError, ValueError, TypeError, AttributeError):
                return [], "LOCAL_OBJECTIVE_NUMBER_OCR_INVALID"
    return output, None
