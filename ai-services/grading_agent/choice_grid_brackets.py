"""开放方括号选项的实测小格；每格必须有成对括号或真实填黑连通域。"""

import cv2
import numpy as np

from .choice_grid_schema import MAX_CELLS


def detect_open_bracket_cells(image):
    """返回裁区像素框和其中的括号格；不外推被遮挡、缺失的选项。"""
    gray = cv2.cvtColor(image, cv2.COLOR_RGB2GRAY)
    ink = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    # 红色批改符号不是印刷括号或铅笔填涂；保留灰阶笔迹，避免批注把相邻格粘成一格。
    annotation = np.max(image, axis=2) - np.min(image, axis=2) > 50
    ink[annotation] = 0
    count, _, stats, _ = cv2.connectedComponentsWithStats(ink, 8)
    if count > 20_000:
        return [], set()
    cells = [tuple(map(int, row[:4])) for row in stats[1:] if row[4] >= 2]
    # 填涂可能通过细桥连到前一格的右括号；只有两侧分别能证明括号和实心块时才拆分。
    # 两个填黑格粘成一片没有括号证据，仍保留为一个连通域，不能按选项数量切开。
    cells = [part for cell in cells for part in _split_attached_bracket(ink, cell)]
    left = [cell for cell in cells if _bracket(ink, cell, True)]
    right = [cell for cell in cells if _bracket(ink, cell, False)]
    partial_left = [cell for cell in cells if cell not in left and _partial_bracket(ink, cell, True)]
    partial_right = [cell for cell in cells if cell not in right and _partial_bracket(ink, cell, False)]
    if len(left) + len(partial_left) > MAX_CELLS or len(right) + len(partial_right) > MAX_CELLS:
        return [], set()
    pairs = set()
    for x, y, width, height in [*left, *partial_left]:
        choices = []
        for other_x, other_y, other_width, other_height in [*right, *partial_right]:
            first, second = (x, y, width, height), (other_x, other_y, other_width, other_height)
            if first in partial_left and (second not in right or not _occluded_end(annotation, first, second)):
                continue
            if second in partial_right and (first not in left or not _occluded_end(annotation, second, first)):
                continue
            total_width = other_x + other_width - x
            if (other_x <= x + width
                    or abs(y + height / 2 - other_y - other_height / 2) > max(1, height * .3)
                    or abs(height - other_height) > max(2, height * .25)
                    or not 2 <= total_width / max(height, other_height) <= 3.8):
                continue
            interior = ink[max(y, other_y):min(y + height, other_y + other_height), x + width:other_x]
            if np.any(interior):
                top = min(y, other_y)
                choices.append((x, top, total_width, max(y + height, other_y + other_height) - top))
        # 一个左括号出现多个可配对右括号时，证据不唯一，整格留给人工。
        if len(choices) == 1:
            pairs.add(choices[0])
    # 字母 A 等有时也像左括号，会跨越相邻 [A]、[B] 配对；先排除覆盖更短实测配对的长框。
    # 相同尺度的配对仍有重叠时不猜测其用途，全部留给人工核对。
    pairs = {cell for cell in pairs if not any(
        other != cell and _intersects(cell, other) and other[2] < cell[2] * .8 for other in pairs)}
    brackets = {cell for cell in pairs if not any(other != cell and _intersects(cell, other) for other in pairs)}
    if len(brackets) < 2 or len(brackets) > MAX_CELLS:
        return [], set()
    typical_width = float(np.median([cell[2] for cell in brackets]))
    typical_height = float(np.median([cell[3] for cell in brackets]))
    measured = list(brackets)
    for x, y, width, height in cells:
        area = int(np.count_nonzero(ink[y:y + height, x:x + width]))
        if (typical_width * .7 <= width <= typical_width * 1.8
                and typical_height * .65 <= height <= typical_height * 2
                and area / (width * height) >= .5
                and x > 0 and y > 0 and x + width < image.shape[1] and y + height < image.shape[0]
                and not any(_intersects((x, y, width, height), cell) for cell in brackets)):
            measured.append((x, y, width, height))
    if len(measured) > MAX_CELLS:
        return [], set()
    return measured, brackets


def _split_attached_bracket(ink, cell):
    x, y, width, height = cell
    if not (10 <= width <= height * 5 and 5 <= height <= 50):
        return [cell]
    pixels = ink[y:y + height, x:x + width]
    candidates = []
    # 切口必须是实测投影谷值，且只移除至多一列细桥像素；不移动或补画原图边缘。
    for cut in range(2, width - 2):
        column_ink = int(np.count_nonzero(pixels[:, cut]))
        if column_ink > max(1, height * .4):
            continue
        parts = []
        for start, end in [(0, cut), (cut + 1, width)]:
            ys, xs = np.nonzero(pixels[:, start:end])
            if not xs.size:
                break
            parts.append((x + start + int(xs.min()), y + int(ys.min()),
                          int(xs.max() - xs.min() + 1), int(ys.max() - ys.min() + 1)))
        if len(parts) != 2:
            continue
        first, second = parts
        for bracket, filled, is_left in [(first, second, False), (second, first, True)]:
            bx, by, bw, bh = filled
            density = np.count_nonzero(ink[by:by + bh, bx:bx + bw]) / (bw * bh)
            if (_bracket(ink, bracket, is_left) and density >= .65
                    and filled[2] >= height * 1.2 and filled[3] >= height * .65):
                candidates.append((column_ink, abs(bracket[2] - bracket[3] * .5), parts))
    return min(candidates, key=lambda item: item[:2])[2] if candidates else [cell]


def _bracket(ink, cell, is_left):
    x, y, width, height = cell
    if not (2 <= width <= height * .85 and 5 <= height <= 50):
        return False
    pixels = ink[y:y + height, x:x + width] > 0
    edge = max(1, round(height * .2))
    middle = pixels[edge:-edge]
    half = max(1, (width + 1) // 2)
    stem = middle[:, :half] if is_left else middle[:, -half:]
    opening = middle[:, -1:] if is_left else middle[:, :1]
    # 扫描缩放会在顶、底多出一行孤立像素，端部证据取邻近两三行，不依赖单行恰好落在笔画上。
    return (float(np.max(np.mean(stem, axis=0))) >= .75 and float(np.mean(opening)) <= .45
            and float(np.max(np.mean(pixels[:edge + 1], axis=1))) >= .5
            and float(np.max(np.mean(pixels[-edge - 1:], axis=1))) >= .5)


def _partial_bracket(ink, cell, is_left):
    """只接收剩余竖干和一个端部；缺失端仍须由完整对侧和真实彩色遮挡共同证明。"""
    x, y, width, height = cell
    if not (2 <= width <= height * .85 and 5 <= height <= 50):
        return False
    pixels = ink[y:y + height, x:x + width] > 0
    edge = max(1, round(height * .2))
    middle = pixels[edge:-edge]
    half = max(1, (width + 1) // 2)
    stem = middle[:, :half] if is_left else middle[:, -half:]
    opening = middle[:, -1:] if is_left else middle[:, :1]
    top = float(np.max(np.mean(pixels[:edge + 1], axis=1))) >= .5
    bottom = float(np.max(np.mean(pixels[-edge - 1:], axis=1))) >= .5
    return (top != bottom and float(np.max(np.mean(stem, axis=0))) >= .8
            and float(np.mean(opening)) <= .45)


def _occluded_end(annotation, partial, complete):
    x, y, width, height = partial
    _, other_y, _, other_height = complete
    # 不外推另一侧端点：完整括号提供本格实测高度，缺口处必须正好存在覆盖笔迹。
    # 返回的格框仍是两侧黑色连通域并集，不延伸到批注本身，也不把纯红 [ABCD] 当小格。
    target = other_y if y > other_y else other_y + other_height - 1
    if target < y - 3 or target > y + height + 2:
        return False
    band = annotation[max(0, target - 1):target + 2, x:x + width]
    return bool(band.size and np.max(np.mean(band, axis=1)) >= .5)


def _intersects(first, second):
    return (min(first[0] + first[2], second[0] + second[2]) > max(first[0], second[0])
            and min(first[1] + first[3], second[1] + second[3]) > max(first[1], second[1]))
