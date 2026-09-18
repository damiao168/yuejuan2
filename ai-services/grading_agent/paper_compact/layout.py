"""OCR geometry and bounded reading-order policies."""

import math


def _box(item):
    value = item[1].get("bbox")
    if not isinstance(value, (list, tuple)) or len(value) != 4:
        return None
    try:
        x, y, width, height = (float(part) for part in value)
    except (TypeError, ValueError):
        return None
    if not all(math.isfinite(part) for part in (x, y, width, height)):
        return None
    if width <= 0 or height <= 0:
        return None
    return x, y, width, height


def _reading_order(items, depth=0):
    """Bounded recursive XY cut with a stable geometric fallback."""

    if len(items) < 8 or depth >= 3:
        return _sort_lines(items)
    partition = _column_partition(items)
    if partition is None:
        return _sort_lines(items)
    left, right, spanning = partition
    if not spanning:
        return _reading_order(left, depth + 1) + _reading_order(right, depth + 1)

    ordered = []
    remaining_left = list(left)
    remaining_right = list(right)
    for separator in sorted(spanning, key=_geometric_key):
        separator_box = _box(separator)
        separator_y = separator_box[1] if separator_box else float("inf")
        before_left = [item for item in remaining_left if _center_y(item) < separator_y]
        before_right = [
            item for item in remaining_right if _center_y(item) < separator_y
        ]
        ordered.extend(_reading_order(before_left, depth + 1))
        ordered.extend(_reading_order(before_right, depth + 1))
        remaining_left = [item for item in remaining_left if item not in before_left]
        remaining_right = [item for item in remaining_right if item not in before_right]
        ordered.append(separator)
    ordered.extend(_reading_order(remaining_left, depth + 1))
    ordered.extend(_reading_order(remaining_right, depth + 1))
    return ordered


def _sort_lines(items):
    lines = []
    unboxed = []
    for item in sorted(items, key=_geometric_key):
        box = _box(item)
        if box is None:
            unboxed.append(item)
            continue
        center = box[1] + box[3] / 2
        best = None
        best_distance = float("inf")
        for line in lines:
            distance = abs(center - line["center"])
            threshold = max(8.0, min(box[3], line["median_height"]) * 0.75)
            if distance <= threshold and distance < best_distance:
                best = line
                best_distance = distance
        if best is None:
            lines.append(
                {
                    "center": center,
                    "median_height": box[3],
                    "items": [(item, box)],
                }
            )
            continue
        best["items"].append((item, box))
        centers = [value[1][1] + value[1][3] / 2 for value in best["items"]]
        heights = sorted(value[1][3] for value in best["items"])
        best["center"] = sum(centers) / len(centers)
        best["median_height"] = heights[len(heights) // 2]
    ordered = []
    for line in sorted(lines, key=lambda value: value["center"]):
        ordered.extend(
            item for item, _ in sorted(line["items"], key=lambda value: value[1][0])
        )
    ordered.extend(unboxed)
    return ordered


def _column_partition(items):
    boxed = [(item, _box(item)) for item in items]
    boxed = [(item, box) for item, box in boxed if box is not None]
    if len(boxed) < 8:
        return None
    page_left = min(box[0] for _, box in boxed)
    page_right = max(box[0] + box[2] for _, box in boxed)
    page_width = page_right - page_left
    if page_width <= 0:
        return None
    candidates = []
    for step in range(15, 86):
        split = page_left + page_width * step / 100
        crossing = sum(1 for _, box in boxed if box[0] < split < box[0] + box[2])
        left_boxes = [box for _, box in boxed if box[0] + box[2] <= split]
        right_boxes = [box for _, box in boxed if box[0] >= split]
        if len(left_boxes) < 4 or len(right_boxes) < 4:
            continue
        left_x = sorted(box[0] for box in left_boxes)[len(left_boxes) // 2]
        right_x = sorted(box[0] for box in right_boxes)[len(right_boxes) // 2]
        if right_x - left_x < page_width * 0.25:
            continue
        balance = min(len(left_boxes), len(right_boxes)) / max(
            len(left_boxes), len(right_boxes)
        )
        # Prefer a clear whitespace gutter, then a well-supported split. Sampling
        # every one percent also handles three-column pages through recursion.
        candidates.append((crossing, -balance, abs(step - 50), split))
    for crossing, _, _, split in sorted(candidates):
        if crossing > max(3, int(len(boxed) * 0.05)):
            continue
        left = []
        right = []
        spanning = []
        for item in items:
            box = _box(item)
            if box is None:
                spanning.append(item)
                continue
            crosses = box[0] < split < box[0] + box[2]
            center = box[0] + box[2] / 2
            if crosses:
                spanning.append(item)
            elif center <= split:
                left.append(item)
            else:
                right.append(item)
        if len(left) < 4 or len(right) < 4:
            continue
        if _vertical_overlap(left, right) < 0.25:
            continue
        return left, right, spanning
    return None


def _vertical_overlap(left, right):
    left_boxes = [_box(item) for item in left]
    right_boxes = [_box(item) for item in right]
    left_boxes = [box for box in left_boxes if box]
    right_boxes = [box for box in right_boxes if box]
    if not left_boxes or not right_boxes:
        return 0
    left_min = min(box[1] for box in left_boxes)
    left_max = max(box[1] + box[3] for box in left_boxes)
    right_min = min(box[1] for box in right_boxes)
    right_max = max(box[1] + box[3] for box in right_boxes)
    overlap = max(0.0, min(left_max, right_max) - max(left_min, right_min))
    smaller = min(left_max - left_min, right_max - right_min)
    return overlap / smaller if smaller > 0 else 0


def _center_y(item):
    box = _box(item)
    return box[1] + box[3] / 2 if box else float(item[0])


def _geometric_key(item):
    box = _box(item)
    # Formula crops are deliberately padded and can start above their host text
    # line. Their vertical centre is a better baseline proxy than the top edge.
    return (box[1] + box[3] / 2, box[0], item[0]) if box else (float("inf"), 0, item[0])
