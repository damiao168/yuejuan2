"""用印刷标题、括号题号和实测横线补充遗漏的填空题候选。

只返回正式填空题清单内的题号；模型实际观察可触发补读，但不能代替印刷题号和横线证据。
本模块只输出候选，不自行关联或确认；主流程核对印刷标题、题号、横线及最终布局四项证据，
完整且无冲突时可自动关联，缺失或矛盾时保留待核对状态。
"""

import csv
import hashlib
import io
import math
import os
import re
import shutil
import subprocess
import time
from collections import defaultdict
from itertools import pairwise

import cv2

FILL_REGION_VERSION = "printed-fill-underline-v5"
_MAX_SECTIONS = 4
_PAGE_SECONDS = 6.0
_SECTION_SECONDS = 2.0
_MIN_CONFIDENCE = 45
_MARKER = re.compile(r"[（(]\s*(\d{1,3})\s*[）)](?:\s*题)?")
# 标题末字可能被 OCR 单独漏掉；“填空”本身已标明题型，题号和横线仍须分别实测。
_HEADINGS = ("填空题", "填空", "fillintheblank", "fillintheblanks")


def discover_fill_regions(image, page, frames, fill_labels, *, observed_fill_labels=(), ocr=None, clock=None):
    """返回独立局部建议及固定失败码；实际观察只用于安排有实测横线的窄带补读。"""
    allowed = {label for label in fill_labels if re.fullmatch(r"\d{1,3}", label)}
    if not allowed:
        return [], []
    observed = allowed.intersection(observed_fill_labels)
    now = clock or time.monotonic
    deadline = now() + _PAGE_SECONDS
    # 测量可降采样，但结果按测量图尺寸归一化，仍对应原来源显示页，不修改画布坐标。
    scale = min(1.0, 2400 / max(image.shape[:2]))
    measured = image if scale == 1 else cv2.resize(image, None, fx=scale, fy=scale, interpolation=cv2.INTER_AREA)
    height, width = measured.shape[:2]
    sections, lines = _measured_sections(measured, frames)
    reader, candidates, reasons, calls = ocr or _read_section, [], [], 0
    for index, section in enumerate(sections):
        remaining = deadline - now()
        if index >= _MAX_SECTIONS or calls >= _MAX_SECTIONS or remaining <= .05:
            reasons.append("LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED")
            break
        left, top, right, bottom = section
        crop = measured[top:bottom, left:right]
        try:
            calls += 1
            tokens, reason = reader(crop, timeout=min(_SECTION_SECONDS, remaining))
        except (OSError, ValueError, cv2.error):
            tokens, reason = [], "LOCAL_FILL_RECOVERY_OCR_FAILED"
        if reason:
            reasons.append(reason)
            continue
        remaining = deadline - now()
        if remaining <= .05:
            reasons.append("LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED")
            break
        tokens = [{**token, "x": token["x"] + left, "y": token["y"] + top} for token in tokens]
        section_tokens = list(tokens)
        found = _section_candidates(tokens, section, lines, allowed, page, width, height)
        missing = _visible_unresolved_markers(tokens, section, lines, allowed, found)
        if missing and calls < _MAX_SECTIONS and remaining > .05:
            # 仅重读已看到括号标记且紧随真实横线的位置；不依据正式题数猜测还缺哪题。
            # 第二次局部 OCR 也消耗全页四次调用、六秒的共同预算。
            enlarged = cv2.resize(crop, None, fx=2, fy=2, interpolation=cv2.INTER_CUBIC)
            try:
                calls += 1
                extra, reason = reader(enlarged, timeout=min(_SECTION_SECONDS, remaining))
            except (OSError, ValueError, cv2.error):
                extra, reason = [], "LOCAL_FILL_RECOVERY_OCR_FAILED"
            if reason:
                reasons.append(reason)
            elif now() >= deadline:
                reasons.append("LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED")
            else:
                extra = [{**token, **{key: token[key] / 2 for key in ("x", "y", "width", "height")},
                          "x": token["x"] / 2 + left, "y": token["y"] / 2 + top} for token in extra]
                # 同一分区放大后，OCR 可能补回题号却漏掉首轮已读出的标题。
                # 只复用本分区已验证的标题下缘；题号、置信度和横线仍由本轮独立核实。
                reread = _section_candidates(
                    extra, section, lines, allowed, page, width, height,
                    verified_header_bottom=_header_bottom(tokens, section))
                # 放大 OCR 可能只补回独立“题”字，却把原本清楚的题号读坏或粘到边线。
                # 仅把同分区实测的这个静态后缀用于重验首轮题号；不复用低置信题号或移动横线。
                suffixes = [token for token in extra if token["text"].strip() == "题"
                            and token["confidence"] >= _MIN_CONFIDENCE]
                section_tokens = [*tokens, *suffixes]
                corroborated = _section_candidates(
                    [*tokens, *suffixes], section, lines, allowed, page, width, height)
                reread = {candidate["printed_question_label"]: candidate for candidate in reread}
                reread.update({candidate["printed_question_label"]: candidate for candidate in corroborated})
                for candidate in reread.values():
                    if candidate["printed_question_label"] in missing:
                        candidate["reason_codes"].append("LOCAL_FILL_LOCAL_OCR_RECHECKED")
                        found.append(candidate)
        # 整区 OCR 可能把下一行的印刷题号和手写答案粘成一个长词；放大整区也不能分开。
        # 已有可靠子题时，只重读尚未对应的实测短横线左侧；模型实际看到的漏题也可触发补读，
        # 但正式清单中的缺题不能触发。窄带题号仍须通过同分区标题、置信度和横线核验。
        for bounds in _unassigned_marker_strips(section, lines, tokens, found, allowed, width, height,
                                               observed_fill_labels=observed):
            remaining = deadline - now()
            if calls >= _MAX_SECTIONS or remaining <= .05:
                reasons.append("LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED")
                break
            strip_left, strip_top, strip_right, strip_bottom = bounds
            try:
                calls += 1
                extra, reason = reader(measured[strip_top:strip_bottom, strip_left:strip_right],
                                       timeout=min(_SECTION_SECONDS, remaining))
            except (OSError, ValueError, cv2.error):
                extra, reason = [], "LOCAL_FILL_RECOVERY_OCR_FAILED"
            if reason:
                reasons.append(reason)
                continue
            if now() >= deadline:
                reasons.append("LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED")
                break
            extra = [{**token, "x": token["x"] + strip_left, "y": token["y"] + strip_top} for token in extra]
            refreshed = {match.group(1) for token in extra
                         if (match := _MARKER.fullmatch(token["text"].strip()))
                         and token["confidence"] >= _MIN_CONFIDENCE}
            # 已恢复的相邻印刷标记仍用于计算行间界线；只替换本窄带重读的题号，不能把14的上沿退回整区顶部。
            section_tokens = [token for token in section_tokens
                              if not ((match := _MARKER.fullmatch(token["text"].strip()))
                                      and match.group(1) in refreshed)] + extra
            reread = _section_candidates(section_tokens, section, lines, allowed, page, width, height,
                                         verified_header_bottom=_header_bottom(tokens, section))
            existing = {candidate["printed_question_label"] for candidate in found}
            reread_counts = defaultdict(int)
            for candidate in reread:
                reread_counts[candidate["printed_question_label"]] += 1
            if not any(candidate["printed_question_label"] not in existing
                       and reread_counts[candidate["printed_question_label"]] == 1 for candidate in reread):
                continue
            refreshed_by_label = {candidate["printed_question_label"]: candidate for candidate in reread
                                  if reread_counts[candidate["printed_question_label"]] == 1}
            # 新恢复下行后，上行半字号余量也要按同一中界收回；只追加下行会留下子题重叠。
            found = [{**refreshed_by_label[candidate["printed_question_label"]],
                      "reason_codes": list(candidate["reason_codes"])}
                     if candidate["printed_question_label"] in refreshed_by_label else candidate for candidate in found]
            for candidate in reread:
                if candidate["printed_question_label"] not in existing and reread_counts[candidate["printed_question_label"]] == 1:
                    candidate["reason_codes"].extend(["LOCAL_FILL_LOCAL_OCR_RECHECKED", "LOCAL_FILL_MARKER_STRIP_VERIFIED"])
                    found.append(candidate)
        candidates.extend(found)
    # 同一题号若出现在两个局部位置，不能根据顺序或置信度任选一个。
    counts = defaultdict(int)
    for candidate in candidates:
        counts[candidate["printed_question_label"]] += 1
    return [candidate for candidate in candidates if counts[candidate["printed_question_label"]] == 1], list(dict.fromkeys(reasons))


def _measured_sections(image, frames):
    height, width = image.shape[:2]
    ink = cv2.threshold(cv2.cvtColor(image, cv2.COLOR_RGB2GRAY), 0, 255,
                        cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    horizontal = cv2.morphologyEx(ink, cv2.MORPH_OPEN,
                                cv2.getStructuringElement(cv2.MORPH_RECT, (max(12, width // 60), 1)))
    _, _, stats, _ = cv2.connectedComponentsWithStats(horizontal)
    lines = [tuple(map(int, row[:4])) for row in stats[1:]
             if row[2] >= max(12, width * .02) and row[3] <= max(3, height * .004)]
    sections = set()
    for frame in frames:
        box = frame["bbox"]
        left, top = math.floor(box["x"] * width), math.floor(box["y"] * height)
        right = math.ceil((box["x"] + box["width"]) * width)
        bottom = math.ceil((box["y"] + box["height"]) * height)
        span = right - left
        if span < width * .08:
            continue
        separators = sorted((y, h) for x, y, w, h in lines
                            if top - 3 <= y <= bottom + 3
                            and max(0, min(right, x + w) - max(left, x)) / span >= .85)
        merged = []
        for y, h in separators:
            if merged and y <= merged[-1][1] + 3:
                merged[-1] = (merged[-1][0], max(merged[-1][1], y + h))
            else:
                merged.append((y, y + h))
        for before, after in pairwise(merged):
            start, end = max(top, before[1]), min(bottom, after[0])
            # 这里只处理有真实上下分隔线的矮分区；整栏计算过程不进入局部填空 OCR。
            if not max(24, height * .02) <= end - start <= span * .8:
                continue
            underlines = [line for line in lines if _inside_line(line, (left, start, right, end))
                          and span * .08 <= line[2] <= span * .70]
            if underlines:
                sections.add((left, start, right, end))
    return sorted(sections, key=lambda section: ((section[3] - section[1]) / (section[2] - section[0]), section[1], section[0])), lines


def _inside_line(line, section):
    x, y, width, height = line
    left, top, right, bottom = section
    return left <= x and x + width <= right and top <= y and y + height <= bottom


def _unassigned_marker_strips(section, lines, tokens, found, allowed, page_width, page_height,
                              *, observed_fill_labels=()):
    if not found or _header_bottom(tokens, section) is None:
        return []
    sizes = [token["height"] for token in tokens
             if (match := _MARKER.fullmatch(token["text"].strip())) and match.group(1) in allowed
             and token["confidence"] >= _MIN_CONFIDENCE and token["height"] > 0]
    if not sizes:
        return []
    font = sorted(sizes)[len(sizes) // 2]
    left, top, right, bottom = section
    observed_missing = set(observed_fill_labels) - {candidate["printed_question_label"] for candidate in found}
    strips = []
    for x, y, width, height in lines:
        if not (_inside_line((x, y, width, height), section)
                and (right - left) * .08 <= width <= (right - left) * .70
                and height <= max(3, font * .25)):
            continue
        # 候选已覆盖的横线不重读；窄带止于横线左端，避免再次带入右侧手写答案。
        if any(box["x"] <= x / page_width + 1e-6 and box["y"] <= y / page_height + 1e-6
               and box["x"] + box["width"] >= (x + width) / page_width - 1e-6
               and box["y"] + box["height"] >= (y + height) / page_height - 1e-6
               for box in (candidate["proposed_bbox"] for candidate in found)):
            continue
        # OCR 可能漏掉整块印刷题号，此时模型实际观察只允许重读真实短横线左侧。
        # 未提供实际观察时，仍仅处理横跨题号位与答案位的粘连长词，不按正式题数扫描。
        if not observed_missing and not any(
                token["x"] < x - font * .5 and token["x"] + token["width"] > x
                and token["width"] >= font * 4 and abs(token["y"] + token["height"] - y) <= font
                for token in tokens):
            continue
        bounds = (max(left, math.floor(x - font * 5)), max(top, math.floor(y - font * 1.75)),
                  x - 1, min(bottom, math.floor(y + font * .35)))
        if bounds[2] - bounds[0] >= font * 2 and bounds[3] - bounds[1] >= font:
            strips.append((x, y, width, height, bounds))
    return [bounds for _, _, _, _, bounds in sorted(strips, key=lambda item: (item[1], item[0]))]


def _eligible_lines(marker, lines, section):
    return [line for line in lines if _inside_line(line, section)
            and 0 <= line[0] - marker["x"] - marker["width"] <= marker["height"] * 4
            and abs(line[1] - marker["y"] - marker["height"]) <= marker["height"] * .35
            and marker["height"] * 2 <= line[2] <= (section[2] - section[0]) * .70
            and line[3] <= max(3, marker["height"] * .25)]


def _visible_unresolved_markers(tokens, section, lines, allowed, found):
    header_bottom = _header_bottom(tokens, section)
    if header_bottom is None:
        return set()
    observed = set()
    for token in tokens:
        match = _MARKER.fullmatch(token["text"].strip())
        if (match and match.group(1) in allowed and token["y"] >= header_bottom
                and token["height"] > 0 and len(_eligible_lines(token, lines, section)) == 1):
            observed.add(match.group(1))
    return observed - {candidate["printed_question_label"] for candidate in found}


def _section_candidates(tokens, section, lines, allowed, page, width, height, *, verified_header_bottom=None):
    left, top, right, bottom = section
    header_bottom = _header_bottom(tokens, section)
    if header_bottom is None:
        header_bottom = verified_header_bottom
    if header_bottom is None:
        return []
    markers = []
    for token in tokens:
        match = _MARKER.fullmatch(token["text"].strip())
        if (not match or match.group(1) not in allowed or token["confidence"] < _MIN_CONFIDENCE
                or token["y"] < header_bottom or token["height"] <= 0):
            continue
        # “题”可能被 OCR 分成另一块。裸数字和数学式不接受；只有与带“题”的同字号
        # 印刷标记处于同一填空分区时，才保留缺失该字的括号标记，且仍要求人工核对。
        suffix = "题" in token["text"] or any(
            other["text"].strip() == "题" and other["confidence"] >= _MIN_CONFIDENCE
            and 0 <= other["x"] - token["x"] - token["width"] <= token["height"]
            and abs(other["y"] - token["y"]) <= token["height"] * .4 for other in tokens)
        markers.append({**token, "label": match.group(1), "suffix": suffix})
    anchors = [marker for marker in markers if marker["suffix"]]
    markers = [marker for marker in markers if marker["suffix"] or any(
        .75 <= marker["height"] / anchor["height"] <= 1.30 for anchor in anchors)]
    matched = []
    for marker in markers:
        eligible = _eligible_lines(marker, lines, section)
        if len(eligible) == 1:
            matched.append((marker, eligible[0]))
    rows = []
    for marker, line in sorted(matched, key=lambda item: (item[0]["y"], item[0]["x"])):
        row = next((row for row in rows if abs(row[0][0]["y"] - marker["y"]) <= marker["height"] * .5), None)
        if row is None:
            rows.append([(marker, line)])
        else:
            row.append((marker, line))
    # 第一行上界取实测分隔线，不取 OCR 标题框的下缘；标题和旁边手写分数可能粘连，
    # 用文字框裁上界会切掉横线上方的答案。后续行从上一条实测横线之后开始。
    pending, previous_bottom = [], top
    for row_index, row in enumerate(rows):
        next_bottom = previous_bottom
        for marker, line in row:
            top_margin = max(1, marker["height"] * .12)
            # 横线不是手写笔画的硬边界；左右和底部留半个印刷字号，容纳轻微越线和下伸，
            # 但始终受实测分区限制，不能延伸到下方禁区或声称覆盖任意手写范围。
            answer_margin = max(1, marker["height"] * .5)
            bounds = [max(left, marker["x"] - answer_margin), previous_bottom + top_margin,
                      min(right, line[0] + line[2] + answer_margin),
                      min(bottom, line[1] + line[3] + answer_margin)]
            if bounds[1] >= marker["y"] or bounds[3] <= marker["y"] + marker["height"]:
                continue
            pending.append((marker, bounds, row_index))
            next_bottom = max(next_bottom, line[1] + line[3])
        previous_bottom = next_bottom

    # 相邻行只在横向也重叠时裁到共同中界；左右分开的两列可以保留各自的下伸空间。
    for index, (_, upper, upper_row) in enumerate(pending):
        for _, lower, lower_row in pending[index + 1:]:
            if (upper_row < lower_row and max(upper[0], lower[0]) < min(upper[2], lower[2])
                    and upper[3] > lower[1]):
                boundary = (upper[3] + lower[1]) / 2
                upper[3] = lower[1] = boundary

    result = []
    for marker, bounds, _ in pending:
        box = {"x": bounds[0] / width, "y": bounds[1] / height,
               "width": (bounds[2] - bounds[0]) / width, "height": (bounds[3] - bounds[1]) / height}
        fingerprint = f"{FILL_REGION_VERSION}:{page['sha256']}:{page['page_no']}:{marker['label']}:{bounds}"
        digest = hashlib.sha256(fingerprint.encode()).hexdigest()[:20]
        result.append({"page_no": page["page_no"], "candidate_id": f"local-fill-{page['page_no']}-{digest}",
                       "printed_question_label": marker["label"], "identity_label": None, "role": "answer",
                       "source_bbox": dict(box), "proposed_bbox": box, "association_status": "unassociated",
                       "semantic_check": "needs_review", "origin": "local_detector",
                       "evidence_refs": [f"local-fill:{FILL_REGION_VERSION}:{digest}"],
                       "reason_codes": ["LOCAL_FILL_HEADER_VERIFIED", "LOCAL_FILL_PRINTED_MARKER_VERIFIED",
                                        "LOCAL_FILL_UNDERLINE_VERIFIED", "LOCAL_FILL_REQUIRES_REVIEW"]})
    return result


def _header_bottom(tokens, section):
    grouped = defaultdict(list)
    for token in tokens:
        if token["confidence"] >= _MIN_CONFIDENCE and token["y"] <= section[1] + (section[3] - section[1]) * .25:
            grouped[token["line"]].append(token)
    headers = []
    for line in grouped.values():
        ordered, spans, offset = sorted(line, key=lambda token: token["x"]), [], 0
        for token in ordered:
            text = re.sub(r"[\s:：_\-]+", "", token["text"]).lower()
            spans.append((offset, offset + len(text), text, token))
            offset += len(text)
        joined = "".join(span[2] for span in spans)
        for heading in _HEADINGS:
            start = joined.find(heading)
            if start >= 0:
                # OCR 偶尔把标题和旁边手写答案放在同一行；只用命中静态标题的字，不能把答案底部当标题边界。
                headers.append(max(token["y"] + token["height"] for begin, end, _, token in spans
                                   if begin < start + len(heading) and end > start))
    return max(headers) if headers else None


def _read_section(image, *, timeout):
    executable = shutil.which("tesseract")
    if executable is None:
        return [], "LOCAL_FILL_RECOVERY_OCR_UNAVAILABLE"
    deadline = time.monotonic() + timeout
    height, width = image.shape[:2]
    # 整数倍率减少小印刷字二次插值的笔画差异；目标约 1400 像素，最多四倍且不降低原有分辨率。
    scale = min(4, max(1, round(1400 / max(height, width))))
    crop = cv2.resize(image, None, fx=scale, fy=scale, interpolation=cv2.INTER_CUBIC)
    data = cv2.imencode(".png", cv2.cvtColor(crop, cv2.COLOR_RGB2BGR))[1].tobytes()
    remaining = deadline - time.monotonic()
    if remaining <= .01:
        return [], "LOCAL_FILL_RECOVERY_OCR_TIMEOUT"
    try:
        run = subprocess.run([executable, "stdin", "stdout", "-l", "chi_sim+eng", "--psm", "11", "tsv"],
                             input=data, capture_output=True, timeout=remaining, check=False,
                             env={**os.environ, "OMP_THREAD_LIMIT": "1"})
    except subprocess.TimeoutExpired:
        return [], "LOCAL_FILL_RECOVERY_OCR_TIMEOUT"
    if run.returncode or len(run.stdout) > 2 * 1024 * 1024:
        return [], "LOCAL_FILL_RECOVERY_OCR_FAILED"
    tokens = []
    try:
        reader = csv.DictReader(io.StringIO(run.stdout.decode("utf8", errors="replace")), delimiter="\t", quoting=csv.QUOTE_NONE)
        required = {"level", "page_num", "block_num", "par_num", "line_num", "left", "top", "width", "height", "conf", "text"}
        if not required <= set(reader.fieldnames or []):
            return [], "LOCAL_FILL_RECOVERY_OCR_FAILED"
        for row in reader:
            # Tesseract 异常退出前也可能留下半行 TSV。缺列属于本次局部失败，不能让解析异常拖垮整页分析。
            if None in row or any(row.get(key) is None for key in required):
                return [], "LOCAL_FILL_RECOVERY_OCR_FAILED"
            if row["level"] != "5" or not row["text"].strip():
                continue
            token = {"text": row["text"].strip(), "confidence": float(row["conf"]),
                     "line": tuple(row[key] for key in ("page_num", "block_num", "par_num", "line_num")),
                     **{key: int(row[column]) / scale for key, column in (
                         ("x", "left"), ("y", "top"), ("width", "width"), ("height", "height"))}}
            if (0 <= token["x"] < width and 0 <= token["y"] < height
                    and token["width"] > 0 and token["height"] > 0 and math.isfinite(token["confidence"])):
                tokens.append(token)
    except (KeyError, TypeError, ValueError, AttributeError):
        return [], "LOCAL_FILL_RECOVERY_OCR_FAILED"
    return tokens, None
