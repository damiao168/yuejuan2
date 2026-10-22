"""用静态身份标签和完整填写结构校准已有字段，不识别或记录学生填写值。"""

import copy
import csv
import io
import math
import os
import re
import shutil
import subprocess
import time
from itertools import pairwise
from statistics import median

import cv2
import numpy as np

IDENTITY_ALIGNMENT_VERSION = "static-label-measured-identity-fields-v3"
_KEYS = ("x", "y", "width", "height")
_LABELS = {"姓名": ("姓名", "Name"), "学号": ("学号", "StudentID"),
           "准考证号": ("准考证号", "CandidateNo."), "考号": ("考号",)}
_STATIC_PARTS = {part for aliases in _LABELS.values() for part in aliases}
_STATIC_PARTS.update("姓名学号准考证")
_STATIC_PARTS.update(label[start:stop] for label in _LABELS for start in range(len(label))
                     for stop in range(start + 2, len(label) + 1))
_SECONDS = 4.0


def align_identity_fields(rows, image, page, *, tokens=(), measurement_bbox=None, label_reader=None, deadline=None,
                          label_context=False):
    """唯一标签与完整横线或网格对应才更新已有字段，失败不改该字段。

    measurement_bbox 是本次受控裁图的整页比例坐标；不跨裁图寻找同名字段。
    tokens 使用整页比例，注入 reader 返回裁区像素坐标。最多补读两个静态窄带，
    共用调用方 deadline 且总共最多四秒；本次实测结构的补读结果只在内存缓存。
    """
    result = {"version": IDENTITY_ALIGNMENT_VERSION, "aligned_indices": [], "label_read_count": 0, "reason_codes": []}
    region = _box(measurement_bbox)
    if (region is None or image is None or image.ndim != 3 or image.shape[2] != 3
            or image.shape[:2] != (page.get("height"), page.get("width"))):
        return result
    height, width = image.shape[:2]
    left, top = math.floor(region[0] * width), math.floor(region[1] * height)
    right, bottom = math.ceil(region[2] * width), math.ceil(region[3] * height)
    crop = image[top:bottom, left:right]
    if crop.size == 0 or crop.shape[0] * crop.shape[1] > 500_000:
        return result
    structures, ink = _structures(crop)
    deadline = min(deadline, time.monotonic() + _SECONDS) if deadline is not None else time.monotonic() + _SECONDS
    reader = label_reader or _read_static_labels
    used, updates = set(), {}
    cache, reader_calls = {}, 0
    for index, row in enumerate(rows):
        label = row.get("identity_label")
        original = _box(row.get("proposed_bbox"))
        if row.get("role") != "identity" or label not in _LABELS or original is None:
            continue
        old = (original[0] * width - left, original[1] * height - top,
               original[2] * width - left, original[3] * height - top)
        options = []
        for structure_index, (kind, bounds, band) in enumerate(structures):
            if kind != ("line" if label == "姓名" else "grid") or not _near(old, bounds):
                continue
            matches = _matching_labels(_page_tokens(tokens, left, top, width, height), label, bounds, kind)
            if not matches and structure_index in cache:
                matches = _matching_labels(cache[structure_index], label, bounds, kind)
            if not matches and structure_index not in cache and time.monotonic() < deadline:
                candidates = []
                # 每条实测结构只读其上方真实文字块，避免上一栏填写笔迹污染标签识别。
                for window in _label_windows(ink, band):
                    x0, y0, x1, y1 = window
                    remaining = deadline - time.monotonic()
                    if remaining <= .01 or reader_calls >= 2:
                        break
                    reader_calls += 1
                    try:
                        context = {"label": label} if label_context else {}
                        found, _reason = reader(crop[y0:y1, x0:x1], timeout=remaining, **context)
                    except (OSError, ValueError, subprocess.TimeoutExpired, cv2.error):
                        continue
                    candidates.extend({**token, "x": token["x"] + x0, "y": token["y"] + y0}
                                      for token in found)
                    if _matching_labels(candidates, label, bounds, kind):
                        break
                cache[structure_index] = candidates
                matches = _matching_labels(candidates, label, bounds, kind)
            if len(matches) == 1:
                matched = matches[0]
                padding = max(1, min(3, round((matched[3] - matched[1]) * .12)))
                box = (max(0, min(bounds[0], matched[0]) - padding),
                       max(0, min(bounds[1], matched[1]) - padding),
                       min(crop.shape[1], max(bounds[2], matched[2]) + padding),
                       min(crop.shape[0], max(bounds[3], matched[3]) + padding))
                options.append((structure_index, box))
        if len(options) == 1:
            structure_index, box = options[0]
            updates[index] = (structure_index, {"x": (box[0] + left) / width, "y": (box[1] + top) / height,
                                                "width": (box[2] - box[0]) / width,
                                                "height": (box[3] - box[1]) / height})
            if structure_index in used:
                # 两个旧字段争用同一实测结构时，两者都不能自动校准。
                for other in list(updates):
                    if updates[other][0] == structure_index:
                        updates.pop(other)
            used.add(structure_index)
    for index, (_structure, box) in updates.items():
        rows[index]["proposed_bbox"] = copy.deepcopy(box)
        result["aligned_indices"].append(index)
    if updates:
        result["reason_codes"] = ["LOCAL_IDENTITY_FIELD_STRUCTURE_ALIGNED"]
    result["label_read_count"] = reader_calls
    return result


def align_initial_identity_fields(rows, image, page, *, tokens=(), label_reader=None, deadline=None):
    """首轮已有身份候选按邻近几何建立有界裁窗，再复用静态标签与完整结构校准。

    不改变显式 measurement_bbox 接口的空值语义，不从整页搜标签造字段。每窗最多
    五十万像素，最多四个窗；补读共用四秒与两个窄带上限，失败保留原模型框。
    """
    result = {"version": IDENTITY_ALIGNMENT_VERSION, "aligned_indices": [], "label_read_count": 0,
              "window_count": 0, "reason_codes": []}
    if (image is None or image.ndim != 3 or image.shape[2] != 3
            or image.shape[:2] != (page.get("height"), page.get("width"))):
        return result
    height, width = image.shape[:2]
    windows = []
    for index, row in enumerate(rows):
        original = _box(row.get("proposed_bbox"))
        if row.get("role") != "identity" or row.get("identity_label") not in _LABELS or original is None:
            continue
        x0, y0, x1, y1 = original
        padx = min(.03, max(8 / width, (x1 - x0) * .08))
        pady = min(.08, max(12 / height, (y1 - y0) * 1.5))
        bounds = (max(0, x0 - padx), max(0, y0 - pady), min(1, x1 + padx), min(1, y1 + pady))
        if (bounds[2] - bounds[0]) * width * (bounds[3] - bounds[1]) * height > 500_000:
            continue
        merged = False
        for window in windows:
            old = window["bounds"]
            if min(old[2], bounds[2]) <= max(old[0], bounds[0]) or min(old[3], bounds[3]) <= max(old[1], bounds[1]):
                continue
            # 相邻栏只因补边窗口相交不能合簇，避免错误身份候选把整栏题目拉进静态标签检测。
            if not any(max(0, min(existing[2], x1) - max(existing[0], x0))
                       >= min(existing[2] - existing[0], x1 - x0) * .5 for existing in window["originals"]):
                continue
            union = (min(old[0], bounds[0]), min(old[1], bounds[1]), max(old[2], bounds[2]), max(old[3], bounds[3]))
            if (union[2] - union[0]) * width * (union[3] - union[1]) * height <= 500_000:
                window["bounds"] = union
                window["indices"].append(index)
                window["originals"].append(original)
                merged = True
                break
        if not merged:
            windows.append({"bounds": bounds, "indices": [index], "originals": [original]})
    stop = min(deadline, time.monotonic() + _SECONDS) if deadline is not None else time.monotonic() + _SECONDS
    reader = label_reader or _read_initial_static_labels

    def bounded_reader(crop, *, timeout, label):
        if result["label_read_count"] >= 2 or time.monotonic() >= stop:
            return [], "LOCAL_IDENTITY_OCR_BUDGET_EXHAUSTED"
        result["label_read_count"] += 1
        context = {"label": label} if label_reader is None else {}
        return reader(crop, timeout=min(timeout, max(.01, stop - time.monotonic())), **context)

    updates = {}
    for window in windows[:4]:
        x0, y0, x1, y1 = window["bounds"]
        selected = [copy.deepcopy(rows[index]) for index in window["indices"]]
        metadata = align_identity_fields(selected, image, page, tokens=tokens,
                                         measurement_bbox=dict(zip(_KEYS, (x0, y0, x1 - x0, y1 - y0))),
                                         label_reader=bounded_reader, deadline=stop, label_context=True)
        result["window_count"] += 1
        for local in metadata["aligned_indices"]:
            updates[window["indices"][local]] = selected[local]["proposed_bbox"]
    for index, box in updates.items():
        rows[index]["proposed_bbox"] = box
        result["aligned_indices"].append(index)
    if updates:
        result["reason_codes"] = ["LOCAL_IDENTITY_FIELD_STRUCTURE_ALIGNED"]
    return result


def _box(value):
    if not isinstance(value, dict):
        return None
    values = [value.get(key) for key in _KEYS]
    if any(type(number) not in {int, float} or not math.isfinite(number) for number in values):
        return None
    x, y, width, height = values
    return (x, y, x + width, y + height) if (x >= 0 and y >= 0 and width > 0 and height > 0
                                          and x + width <= 1.000001 and y + height <= 1.000001) else None


def _structures(image):
    gray = cv2.cvtColor(image, cv2.COLOR_RGB2GRAY)
    ink = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    length = max(12, round(image.shape[1] * .07))
    length += length % 2 == 0
    horizontal = cv2.morphologyEx(ink, cv2.MORPH_OPEN, np.ones((1, length), np.uint8))
    _, _, stats, _ = cv2.connectedComponentsWithStats(horizontal)
    lines = [tuple(map(int, row[:4])) for row in stats[1:] if row[2] >= length and row[3] <= 4][:128]
    structures, grid_edges = [], set()
    for first_index, (x, y, width, thick) in enumerate(lines):
        for second_index, (xx, yy, ww, hh) in enumerate(lines):
            gap = yy - y
            if (not 8 <= gap <= min(60, width * .25) or abs(x - xx) > 3
                    or abs(x + width - xx - ww) > 3):
                continue
            band = ink[y:yy + hh, min(x, xx):max(x + width, xx + ww)]
            columns = np.where(np.count_nonzero(band, axis=0) >= band.shape[0] * .75)[0]
            groups = _runs(columns)
            separators = [round(sum(group) / len(group)) for group in groups]
            if not _complete_lattice(separators, band.shape[1]):
                continue
            bounds = (min(x, xx), y, max(x + width, xx + ww), yy + hh)
            # 标签窄带只在完整网格上方，数字或学生填写格内部不参与类别判断。
            structures.append(("grid", bounds, (bounds[0], max(0, y - round(gap * 1.2)), bounds[2], y)))
            grid_edges.update((first_index, second_index))
    for index, (x, y, width, thick) in enumerate(lines):
        if index not in grid_edges:
            structures.append(("line", (x, y, x + width, y + thick),
                               (x, max(0, y - round(width * .32)), x + width, y)))
    return structures, ink


def _runs(values):
    groups = []
    for value in values:
        if not groups or value > groups[-1][-1] + 1:
            groups.append([int(value)])
        else:
            groups[-1].append(int(value))
    return groups


def _complete_lattice(separators, width):
    if len(separators) < 5 or separators[0] > 3 or separators[-1] < width - 4:
        return False
    chains = set()
    typical = median(right - left for left, right in pairwise(separators))
    for second in separators[1:]:
        step = second - separators[0]
        # 不能跳过实际分隔线，把缺格误当作格宽翻倍；填写笔画产生的少量短间距不决定格宽。
        if step < 6 or step > width / 4 or not typical * .75 <= step <= typical * 1.25:
            continue
        chain = [separators[0], second]
        while chain[-1] < width - 4:
            eligible = [value for value in separators if value > chain[-1]
                        and abs(value - chain[-1] - step) <= max(2, step * .16)]
            if len(eligible) != 1:
                break
            chain.append(eligible[0])
        if len(chain) >= 5 and chain[-1] >= width - 4:
            chains.add(tuple(chain))
    return len(chains) == 1


def _near(old, bounds):
    overlap = max(0, min(old[2], bounds[2]) - max(old[0], bounds[0]))
    gap = max(0, bounds[1] - old[3], old[1] - bounds[3])
    return overlap >= min(old[2] - old[0], bounds[2] - bounds[0]) * .6 and gap <= (old[3] - old[1]) * 2


def _page_tokens(tokens, left, top, width, height):
    return [{**token, "x": token["x"] * width - left, "y": token["y"] * height - top,
             "width": token["width"] * width, "height": token["height"] * height}
            for token in tokens if all(type(token.get(key)) in {int, float} for key in _KEYS)]


def _matching_labels(tokens, label, bounds, kind):
    eligible = []
    for token in tokens:
        confidence = token.get("confidence", 100)
        text = re.sub(r"[\s:：_]+", "", token.get("text", ""))
        if confidence >= 45 and text in _STATIC_PARTS and token.get("width", 0) > 0 and token.get("height", 0) > 0:
            eligible.append({**token, "text": text})
    eligible.sort(key=lambda token: (token["y"], token["x"]))
    matches = []
    for first in eligible:
        same_line = sorted([token for token in eligible if token["x"] >= first["x"]
                            and abs(token["y"] - first["y"]) <= max(token["height"], first["height"]) * .35],
                           key=lambda token: token["x"])
        parts = []
        for token in same_line[:6]:
            if parts and (token["x"] < parts[-1]["x"] + parts[-1]["width"] * .7
                          or token["x"] - parts[-1]["x"] - parts[-1]["width"] > token["height"] * 3):
                continue
            parts.append(token)
            if "".join(part["text"] for part in parts) not in _LABELS[label]:
                continue
            box = (min(part["x"] for part in parts), min(part["y"] for part in parts),
                   max(part["x"] + part["width"] for part in parts), max(part["y"] + part["height"] for part in parts))
            font = box[3] - box[1]
            gap = bounds[1] - box[3]
            if (box[0] >= bounds[0] - font * 4 and box[2] <= bounds[2] + font
                    and -font * .2 <= gap <= font * (2.5 if kind == "line" else 1.5)
                    and not any(max(abs(a - b) for a, b in zip(box, old)) <= 2 for old in matches)):
                matches.append(box)
    return matches


def _label_windows(ink, band):
    x0, y0, x1, y1 = band
    if x1 <= x0 or y1 <= y0:
        return []
    _, _, stats, _ = cv2.connectedComponentsWithStats(ink[y0:y1, x0:x1])
    glyphs = [tuple(map(int, row[:4])) for row in stats[1:]
              if 5 <= row[3] <= max(6, (y1 - y0) * .7) and 2 <= row[2] <= (y1 - y0) * 2]
    rows = []
    for glyph in sorted(glyphs, key=lambda box: (box[1] + box[3] / 2, box[0])):
        matching = [row for row in rows if abs(median(part[1] + part[3] / 2 for part in row)
                                                - glyph[1] - glyph[3] / 2) <= max(part[3] for part in row) * .4]
        if len(matching) == 1:
            matching[0].append(glyph)
        else:
            rows.append([glyph])
    windows = []
    for row in rows:
        groups = []
        for glyph in sorted(row):
            if not groups or glyph[0] - groups[-1][-1][0] - groups[-1][-1][2] > max(item[3] for item in row) * 2.6:
                groups.append([glyph])
            else:
                groups[-1].append(glyph)
        for group in groups:
            if len(group) < 2:
                continue
            pad = max(2, round(max(part[3] for part in group) * .3))
            windows.append((max(x0, x0 + min(part[0] for part in group) - pad),
                            max(y0, y0 + min(part[1] for part in group) - pad),
                            min(x1, x0 + max(part[0] + part[2] for part in group) + pad),
                            min(y1, y0 + max(part[1] + part[3] for part in group) + pad)))
    return windows[:4]


def _read_static_labels(image, *, timeout, scales=(2, 3, 4)):
    executable = shutil.which("tesseract")
    if executable is None:
        return [], "LOCAL_IDENTITY_OCR_UNAVAILABLE"
    deadline, tokens = time.monotonic() + min(timeout, _SECONDS), []
    # 整数倍率只补同一静态印刷字的互补证据；保留实际字框且不降低原有置信阈值。
    for scale in scales:
        remaining = deadline - time.monotonic()
        if remaining <= .01:
            break
        resized = cv2.resize(image, None, fx=scale, fy=scale, interpolation=cv2.INTER_CUBIC)
        data = cv2.imencode(".png", cv2.cvtColor(resized, cv2.COLOR_RGB2BGR))[1].tobytes()
        process = subprocess.run([executable, "stdin", "stdout", "-l", "chi_sim+eng", "--psm", "6", "tsv"],
                                 input=data, capture_output=True, timeout=remaining, check=False,
                                 env={**os.environ, "OMP_THREAD_LIMIT": "1"})
        if process.returncode or len(process.stdout) > 1024 * 1024:
            continue
        reader = csv.DictReader(io.StringIO(process.stdout.decode("utf8", errors="replace")), delimiter="\t", quoting=csv.QUOTE_NONE)
        for row in reader:
            try:
                text, confidence = row["text"].strip(), float(row["conf"])
                if row["level"] != "5" or confidence < 45 or text not in _STATIC_PARTS:
                    continue
                token = {"text": text, "confidence": confidence, **{key: int(row[column]) / scale
                         for key, column in (("x", "left"), ("y", "top"), ("width", "width"), ("height", "height"))}}
            except (KeyError, TypeError, ValueError, AttributeError):
                return [], "LOCAL_IDENTITY_OCR_INVALID"
            if not any(old["text"] == text and max(abs(old[key] - token[key]) for key in _KEYS) <= 2 for old in tokens):
                tokens.append(token)
    return tokens, None


def _read_initial_static_labels(image, *, timeout, label=None):
    deadline = time.monotonic() + min(timeout, _SECONDS)
    tokens, reason = _read_static_labels(image, timeout=max(.01, deadline - time.monotonic()), scales=(2, 4))
    if deadline - time.monotonic() <= .01:
        return tokens, reason
    parts = {token["text"] for token in tokens}
    expected = [label] if label in _LABELS else _LABELS
    # “准考证号”窄带也可能读到“考”“号”；不能因另一个较短类别凑齐就提前停止补字。
    if any(expected_label in parts or all(character in parts for character in expected_label) for expected_label in expected):
        return tokens, reason
    # 低分辨率汉字的灰边会让“准”误读；同一窄带二值化只补精确静态字，不纠正或猜测错字。
    gray = cv2.cvtColor(image, cv2.COLOR_RGB2GRAY)
    # Otsu 会受窄带里的横线和 JPEG 灰边影响；固定暗墨副本保留细字间隙，原灰度证据仍保留。
    binary = cv2.threshold(gray, 150, 255, cv2.THRESH_BINARY)[1]
    extra, _reason = _read_static_labels(cv2.cvtColor(binary, cv2.COLOR_GRAY2RGB),
                                        timeout=max(.01, deadline - time.monotonic()), scales=(4,))
    # 同一字在灰度与暗墨副本重复出现时只占一个位置，避免重复候选挤掉后面的完整标签字。
    for token in extra:
        if not any(old["text"] == token["text"] and max(abs(old[key] - token[key]) for key in _KEYS) <= 2 for old in tokens):
            tokens.append(token)
    return tokens, reason
