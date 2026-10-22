"""对本次整页结果做保守复核，不调用模型，也不代替人工判断版面。"""

from itertools import pairwise

from .answer_sheet_vision import snap_to_printed_frame

SYNTHETIC_PARTITION_REASON = "SUSPECTED_SYNTHETIC_PAGE_PARTITION"


def review_synthetic_page_partition(rows, question_labels, printed_frames):
    """疑似按整场题号均分页时撤销自动关联，保留坐标和题号供人工核对。"""
    answers = [row for row in rows if row["role"] == "answer"]
    allowed = set(question_labels)
    observed = {row["printed_question_label"] for row in answers}
    if len(answers) < 6 or not allowed or len(observed & allowed) / len(allowed) < 0.9:
        return False
    # 模型也能返回原因码，不能信任它自称已验证。只以本次 OpenCV 线框支持豁免真实行表。
    if any(snap_to_printed_frame(row["proposed_bbox"], printed_frames)[2] == "PRINTED_FRAME_VERIFIED"
           for row in answers):
        return False
    boxes = sorted((row["proposed_bbox"] for row in answers), key=lambda box: box["y"])
    if any(box["x"] > 0.1 or box["width"] < 0.8 for box in boxes):
        return False
    if (max(box["x"] for box in boxes) - min(box["x"] for box in boxes) > 0.02
            or max(box["width"] for box in boxes) - min(box["width"] for box in boxes) > 0.02):
        return False
    mean_height = sum(box["height"] for box in boxes) / len(boxes)
    if any(abs(box["height"] - mean_height) > max(0.002, mean_height * 0.08) for box in boxes):
        return False
    # 坐标均为 0～1 页面比例。只标记从页首附近连续铺满至少八成页面的宽横条，避免泛化成无框即失败。
    if boxes[0]["y"] > 0.15 or boxes[-1]["y"] + boxes[-1]["height"] - boxes[0]["y"] < 0.8:
        return False
    if any(abs(second["y"] - first["y"] - first["height"]) > max(0.003, mean_height * 0.15)
           for first, second in pairwise(boxes)):
        return False
    for row in answers:
        if SYNTHETIC_PARTITION_REASON not in row["reason_codes"]:
            row["reason_codes"].append(SYNTHETIC_PARTITION_REASON)
        if row["association_status"] == "associated":
            row["association_status"] = "ambiguous"
    return True
