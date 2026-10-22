"""从人工大框中的实测轮廓分组选项；不按题目数或选项数生成缺失格子。"""

import math
from itertools import pairwise

import cv2
import numpy as np

from .choice_grid_brackets import detect_open_bracket_cells
from .choice_grid_schema import MAX_CELLS, MAX_GROUPS


def detect_option_groups(image, option_count, orientation):
    """返回裁区像素坐标的选项组及复核原因，题号和 A/B/C/D 映射由人工确认。"""
    height, width = image.shape[:2]
    if min(width, height) < 12:
        return [], ["OBJECTIVE_BLOCK_TOO_SMALL"]
    gray = cv2.cvtColor(image, cv2.COLOR_RGB2GRAY)
    ink = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    contours, _ = cv2.findContours(ink, cv2.RETR_TREE, cv2.CHAIN_APPROX_SIMPLE)
    if len(contours) > 20_000:
        return [], ["TOO_MANY_OPTION_CANDIDATES"]
    cells, clipped = _option_cells(contours, width, height, option_count, orientation)
    if len(cells) > MAX_CELLS:
        return [], ["TOO_MANY_OPTION_CANDIDATES"]
    reasons = ["CLIPPED_OPTION_CANDIDATES"] if clipped else []
    groups = []
    along, cross, along_size, cross_size = (0, 1, 2, 3) if orientation == "horizontal" else (1, 0, 3, 2)
    for cluster in _size_clusters(cells):
        for line in _aligned_lines(cluster, cross, cross_size):
            line.sort(key=lambda cell: _center(cell, along, along_size))
            for segment in _separate_groups(line, along, along_size):
                if len(segment) != option_count:
                    reasons.append("OPTION_COUNT_OR_GROUPING_UNCERTAIN")
                elif not _regular_group(segment, along, cross, along_size, cross_size):
                    reasons.append("OPTION_SPACING_OR_SIZE_UNCERTAIN")
                else:
                    groups.append(segment)
    # 内部字母若也形成闭合轮廓，同序且逐格嵌套的组只留外层；不把整题外框当单个小格吞掉内部真实格子。
    groups = [group for index, group in enumerate(groups) if not any(
        index != other_index and _group_contains(other, group) for other_index, other in enumerate(groups))]
    # 不同形状簇若生成相互覆盖的题区，不能任意选一种映射；所有冲突组均留待手动画格。
    conflicts = {index for index, group in enumerate(groups) for other_index, other in enumerate(groups)
                 if index != other_index and _groups_overlap(group, other)}
    if conflicts:
        reasons.append("OPTION_GROUP_CONFLICT")
    groups = [group for index, group in enumerate(groups) if index not in conflicts]
    # 同一大区可能混有闭合格和 [A] 开放括号，已有闭合组不能让其他行跳过括号检测。
    # 只追加不与已测组重叠的结果；每一格仍须有成对括号或填黑连通域，不能按题数补格。
    measured, bracket_cells = detect_open_bracket_cells(image)
    bracket_groups = []
    for line in _aligned_lines(measured, cross, cross_size):
        line.sort(key=lambda cell: _center(cell, along, along_size))
        for segment in _separate_groups(line, along, along_size):
            if (len(segment) == option_count and sum(cell in bracket_cells for cell in segment) >= 2
                    and _regular_bracket_group(segment, along, cross, along_size, cross_size)):
                bracket_groups.append(segment)
            else:
                reasons.append("OPTION_COUNT_OR_GROUPING_UNCERTAIN")
    bracket_groups = [group for group in bracket_groups if not any(_groups_overlap(group, other) for other in groups)]
    if bracket_groups:
        reasons.append("OPEN_BRACKET_GROUPS_REQUIRE_REVIEW")
    conflicts = {index for index, group in enumerate(bracket_groups) for other_index, other in enumerate(bracket_groups)
                 if index != other_index and _groups_overlap(group, other)}
    if conflicts:
        reasons.append("OPTION_GROUP_CONFLICT")
    groups.extend(group for index, group in enumerate(bracket_groups) if index not in conflicts)
    if len(groups) > MAX_GROUPS:
        return [], ["TOO_MANY_OPTION_GROUPS"]
    if not cells and not measured:
        reasons.append("NO_RELIABLE_OPTION_CELLS")
    if not groups:
        reasons.append("NO_RELIABLE_OPTION_GROUPS")
    return sorted(groups, key=lambda group: (min(cell[1] for cell in group), min(cell[0] for cell in group))), list(dict.fromkeys(reasons))


def _option_cells(contours, width, height, option_count, orientation):
    candidates, clipped = [], False
    for contour in contours:
        x, y, w, h = cv2.boundingRect(contour)
        # 至少六像素才有可测内区；形状和大框比例上限排除文字长线、题区外框，不依赖固定扫描 DPI。
        along_fraction = min(.6, 1 / (option_count * .8))
        max_width, max_height = (width * along_fraction, height * .9) if orientation == "horizontal" else (width * .9, height * along_fraction)
        if min(w, h) < 6 or w > max(12, max_width) or h > max(12, max_height) or not .35 <= w / h <= 3.5:
            continue
        area = cv2.contourArea(contour)
        hull_area = cv2.contourArea(cv2.convexHull(contour))
        if area <= 0 or hull_area <= 0 or area / hull_area < .88:
            continue
        perimeter = cv2.arcLength(contour, True)
        polygon = cv2.approxPolyDP(contour, .035 * perimeter, True)
        rectangle = len(polygon) == 4 and cv2.isContourConvex(polygon) and area / (w * h) >= .70
        ellipse = False
        if len(contour) >= 5 and area / (w * h) >= .55:
            _, axes, _ = cv2.fitEllipse(contour)
            ellipse_area = math.pi * axes[0] * axes[1] / 4
            ellipse = ellipse_area > 0 and .80 <= area / ellipse_area <= 1.15
        if not rectangle and not ellipse:
            continue
        if x <= 0 or y <= 0 or x + w >= width or y + h >= height:
            clipped = True
            continue
        candidates.append((x, y, w, h))
    # 空心轮廓有内外两层，只合并尺度相近的嵌套边缘；题区大外框不能吞掉内部小格。
    distinct = []
    for cell in sorted(candidates, key=lambda item: -item[2] * item[3]):
        if not any(_contains(other, cell) and cell[2] >= other[2] * .5 and cell[3] >= other[3] * .5 for other in distinct):
            distinct.append(cell)
    return distinct, clipped


def _contains(outer, inner):
    return (inner[0] >= outer[0] and inner[1] >= outer[1]
            and inner[0] + inner[2] <= outer[0] + outer[2] and inner[1] + inner[3] <= outer[1] + outer[3])


def _size_clusters(cells):
    clusters = []
    for cell in sorted(cells, key=lambda item: -item[2] * item[3]):
        matching = next((cluster for cluster in clusters if all(
            abs(cell[axis] - float(np.median([other[axis] for other in cluster])))
            <= max(2, float(np.median([other[axis] for other in cluster])) * .30) for axis in (2, 3))), None)
        if matching is None:
            clusters.append([cell])
        else:
            matching.append(cell)
    return clusters


def _center(cell, axis, size):
    return cell[axis] + cell[size] / 2


def _aligned_lines(cells, cross, cross_size):
    lines = []
    for cell in sorted(cells, key=lambda item: _center(item, cross, cross_size)):
        matching = next((line for line in lines if abs(_center(cell, cross, cross_size) - float(np.median(
            [_center(other, cross, cross_size) for other in line]))) <= max(2, min(cell[cross_size],
            float(np.median([other[cross_size] for other in line]))) * .4)), None)
        if matching is None:
            lines.append([cell])
        else:
            matching.append(cell)
    return lines


def _separate_groups(line, along, along_size):
    if len(line) < 2:
        return [line]
    gaps = [_center(second, along, along_size) - _center(first, along, along_size) for first, second in pairwise(line)]
    typical = float(np.median(sorted(gaps)[:max(1, math.ceil(len(gaps) / 2))]))
    thickness = float(np.median([cell[along_size] for cell in line]))
    segments = [[line[0]]]
    for index, gap in enumerate(gaps):
        # 组间至少明显大于常规间距和格宽；没有大间隔时不会按 option_count 硬切连续小格。
        if gap > max(typical * 1.75, thickness * 1.8):
            segments.append([])
        segments[-1].append(line[index + 1])
    return segments


def _regular_group(cells, along, cross, along_size, cross_size):
    if any(max(cell[axis] for cell in cells) - min(cell[axis] for cell in cells)
           > max(2, float(np.median([cell[axis] for cell in cells])) * .30) for axis in (2, 3)):
        return False
    gaps = [_center(second, along, along_size) - _center(first, along, along_size) for first, second in pairwise(cells)]
    typical = float(np.median(gaps))
    if any(gap <= max(first[along_size], second[along_size]) * .85 or abs(gap - typical) > max(3, typical * .25)
           for gap, (first, second) in zip(gaps, pairwise(cells), strict=True)):
        return False
    centers = [_center(cell, cross, cross_size) for cell in cells]
    return max(centers) - min(centers) <= max(3, min(cell[cross_size] for cell in cells) * .4)


def _regular_bracket_group(cells, along, cross, along_size, cross_size):
    # 涂黑会比印刷括号略大，检查实测中心间距和共线性；坐标仍取每一格的真实包围框。
    gaps = [_center(second, along, along_size) - _center(first, along, along_size) for first, second in pairwise(cells)]
    typical = float(np.median(gaps))
    if any(gap <= max(first[along_size], second[along_size]) * .75 or abs(gap - typical) > max(3, typical * .25)
           for gap, (first, second) in zip(gaps, pairwise(cells), strict=True)):
        return False
    centers = [_center(cell, cross, cross_size) for cell in cells]
    return max(centers) - min(centers) <= max(3, min(cell[cross_size] for cell in cells) * .5)


def _groups_overlap(first, second):
    left_a, top_a, right_a, bottom_a = _bounds(first)
    left_b, top_b, right_b, bottom_b = _bounds(second)
    return min(right_a, right_b) > max(left_a, left_b) and min(bottom_a, bottom_b) > max(top_a, top_b)


def _group_contains(outer, inner):
    return (sum(cell[2] * cell[3] for cell in outer) > sum(cell[2] * cell[3] for cell in inner)
            and all(_contains(first, second) for first, second in zip(outer, inner, strict=True)))


def _bounds(cells):
    return (min(cell[0] for cell in cells), min(cell[1] for cell in cells),
            max(cell[0] + cell[2] for cell in cells), max(cell[1] + cell[3] for cell in cells))
