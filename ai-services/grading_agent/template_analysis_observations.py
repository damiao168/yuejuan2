"""核对模型自报的有限视觉观察，不把它当作独立 OCR 证据。

必须在角色、题号清理前调用。独立小警示牌可改为说明区，混合作答栏不能隐藏；
矛盾结果保留供纠错，一致结果由后续语义层决定是否自动采用。
"""

import math
import re

from .template_analysis_schema import (
    MAX_OBSERVED_QUESTION_LABELS,
    OBSERVATION_BARCODE_PATTERNS,
    OBSERVATION_BOUNDARY_BASES,
    OBSERVATION_IDENTITY_PATTERNS,
    OBSERVATION_OPTION_PATTERNS,
    OBSERVATION_QUESTION_LABEL_PATTERN,
    OBSERVATION_STATIC_LABELS,
)

OBSERVATION_REVIEW_VERSION = "model-reported-visual-observations-v4"
_OPTION_FEATURES = {"letter_options", "bubble_options", "judgement_options"}
_IDENTITY_FEATURES = {"name_line", "number_grid", "identity_fields"}
_BARCODE_FEATURES = {"linear_bars", "qr_modules", "labelled_placeholder"}
_WARNING_LABELS = {"禁止作答", "禁止答题", "请勿作答", "请勿填写", "注意事项"}
# 填涂示例是说明，不是禁止作答警示；不能借示例标签触发自动排除实际题区。
_INSTRUCTION_LABELS = _WARNING_LABELS | {"正确填涂", "错误填涂", "填涂示例"}
_IDENTITY_LABELS = {"姓名", "学号", "准考证号", "考号", "班级", "学校"}
_BARCODE_LABELS = {"条码", "二维码", "条形码粘贴区", "二维码粘贴区"}
_BARCODE_TYPES = {"条码", "二维码"}
_STATIC_LABEL_CATEGORIES = {
    "Name": "姓名", "Student ID": "学号", "Candidate No.": "准考证号", "Class": "班级", "School": "学校",
    "Barcode": "条码", "QR Code": "二维码", "条形码": "条码", "条码粘贴区": "条码", "条形码区": "条码",
    "条形码粘贴区": "条码", "二维码粘贴区": "二维码",
    "单选题": "选择题", "多选题": "选择题", "单项选择题": "选择题", "多项选择题": "选择题", "判断题": "选择题",
}
_QUESTION_SECTION_LABELS = {"选择题", "填空题", "解答题"}
_LABEL_PATTERN = re.compile(OBSERVATION_QUESTION_LABEL_PATTERN)
_EVIDENCE_KEYS = {"static_label", "option_pattern", "identity_pattern", "barcode_pattern",
                  "boundary_basis", "anchor_bbox"}


def _flag(row, code):
    reasons = row.setdefault("reason_codes", [])
    if code not in reasons:
        reasons.append(code)
    row["semantic_check"] = "needs_review"


def _box(value):
    if not isinstance(value, dict) or set(value) != {"x", "y", "width", "height"}:
        return None
    values = [value[key] for key in ("x", "y", "width", "height")]
    if any(type(number) not in {int, float} or not math.isfinite(number) for number in values):
        return None
    x, y, width, height = values
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1.000001 or y + height > 1.000001:
        return None
    return values


def _review_anchor(row, anchor):
    if anchor is None:
        return
    box = _box(anchor)
    if box is None:
        _flag(row, "MODEL_OBS_ANCHOR_OUT_OF_PAGE")
        return
    candidate = _box(row.get("proposed_bbox"))
    if candidate is None:
        _flag(row, "MODEL_OBS_CANDIDATE_GEOMETRY_INVALID")
        return
    x, y, width, height = box
    cx, cy, cw, ch = candidate
    # 允许标签锚点紧邻边界；水平、竖直间隙分别最多为整页尺寸的 2%，不按框内坐标解释。
    dx = max(cx - x - width, x - cx - cw, 0)
    dy = max(cy - y - height, y - cy - ch, 0)
    if dx > .02 or dy > .02:
        _flag(row, "MODEL_OBS_ANCHOR_OUTSIDE_REGION")


def _valid_observations(labels, evidence):
    if (not isinstance(labels, list) or len(labels) > MAX_OBSERVED_QUESTION_LABELS
            or any(not isinstance(label, str) or len(label) > 7 or not _LABEL_PATTERN.fullmatch(label)
                   for label in labels) or len(labels) != len(set(labels))):
        return False
    if not isinstance(evidence, dict) or set(evidence) != _EVIDENCE_KEYS:
        return False
    static = evidence.get("static_label")
    return (static is None or isinstance(static, str) and static in OBSERVATION_STATIC_LABELS) and all(
        isinstance(evidence.get(key), str) and evidence[key] in allowed for key, allowed in (
            ("option_pattern", OBSERVATION_OPTION_PATTERNS), ("identity_pattern", OBSERVATION_IDENTITY_PATTERNS),
            ("barcode_pattern", OBSERVATION_BARCODE_PATTERNS), ("boundary_basis", OBSERVATION_BOUNDARY_BASES)))


def exclude_isolated_model_warning(row):
    """模型读到独立小警示牌时归为说明区；混合作答大框仍交由正常规则处理。"""
    labels, evidence = row.get("observed_question_labels"), row.get("visual_evidence")
    box = _box(row.get("proposed_bbox"))
    if (not _valid_observations(labels, evidence) or box is None
            or labels or row.get("printed_question_label") is not None
            or evidence["static_label"] not in _WARNING_LABELS
            or any(evidence[key] != "none" for key in ("option_pattern", "identity_pattern", "barcode_pattern"))):
        return False
    _, _, width, height = box
    # 面积和边长均按整页比例计；警示只占大栏一角时不能让整栏及其中填空题消失。
    if width * height > .04 or width > .4 or height > .25:
        return False
    _review_anchor(row, evidence["anchor_bbox"])
    if row.get("semantic_check") == "needs_review":
        return False
    row.update(role="instruction", identity_label=None, association_status="unassociated")
    row["reason_codes"].append("MULTIMODAL_FORBIDDEN_REGION_EXCLUDED")
    return True


def _review_identity(row, evidence, observed_official):
    label = row.get("identity_label")
    static = _STATIC_LABEL_CATEGORIES.get(evidence["static_label"], evidence["static_label"])
    if evidence["option_pattern"] in _OPTION_FEATURES:
        _flag(row, "MODEL_OBS_IDENTITY_OPTION_CONFLICT")
    if observed_official:
        _flag(row, "MODEL_OBS_IDENTITY_QUESTION_CONFLICT")
    if static in _QUESTION_SECTION_LABELS:
        _flag(row, "MODEL_OBS_IDENTITY_STATIC_LABEL_CONFLICT")
    # 同一候选自报多种字段结构时仍保留范围，交给人工区分并排字段，不能任选一种标签。
    if label in _BARCODE_TYPES:
        if static in _IDENTITY_LABELS:
            _flag(row, "MODEL_OBS_IDENTITY_STATIC_LABEL_CONFLICT")
        if evidence["identity_pattern"] in _IDENTITY_FEATURES:
            _flag(row, "MODEL_OBS_BARCODE_IDENTITY_CONFLICT")
    else:
        if static in _BARCODE_LABELS:
            _flag(row, "MODEL_OBS_IDENTITY_STATIC_LABEL_CONFLICT")
        if evidence["barcode_pattern"] in _BARCODE_FEATURES:
            _flag(row, "MODEL_OBS_IDENTITY_BARCODE_CONFLICT")
    if static in _IDENTITY_LABELS and label in _IDENTITY_LABELS and static != label:
        _flag(row, "MODEL_OBS_IDENTITY_LABEL_CONFLICT")
    if label in _BARCODE_TYPES:
        _review_barcode(row, evidence)
        return
    # 横线、编号格也可能是选择题；结构只能说明形状，不能代替实际读到的身份印刷标签。
    # 英文标签只在比较时映射类别，原观察仍保留实际标签，不把中文翻译冒充印刷证据。
    matching_pattern = ((label == "姓名" and evidence["identity_pattern"] == "name_line")
                        or (label in {"学号", "准考证号", "考号"} and evidence["identity_pattern"] == "number_grid")
                        or (label in {"班级", "学校", "身份信息", None}
                            and evidence["identity_pattern"] == "identity_fields"))
    matching_label = static == label if label in _IDENTITY_LABELS else static in _IDENTITY_LABELS
    if not matching_label and not matching_pattern:
        _flag(row, "MODEL_OBS_IDENTITY_EVIDENCE_MISSING")
    elif not matching_label:
        _flag(row, "MODEL_OBS_IDENTITY_LABEL_MISSING")
    anchor, candidate = _box(evidence["anchor_bbox"]), _box(row.get("proposed_bbox"))
    # 真实故障中模型把整条选择题行原样复制为“姓名标签锚点”；没有局部标签位置时先裁图复判。
    if matching_label and anchor and candidate and all(abs(left - right) <= 1e-6 for left, right in zip(anchor, candidate)):
        _flag(row, "MODEL_OBS_IDENTITY_LABEL_ANCHOR_NOT_LOCALIZED")


def _review_barcode(row, evidence):
    pattern = evidence["barcode_pattern"]
    static = _STATIC_LABEL_CATEGORIES.get(evidence["static_label"], evidence["static_label"])
    # 模板贴码区可能尚未贴码；明确标签配合贴附区观察足够保留候选，不能强制解码。
    if pattern not in {"linear_bars", "qr_modules"} and not (
            pattern == "labelled_placeholder" and static in _BARCODE_LABELS):
        _flag(row, "MODEL_OBS_BARCODE_EVIDENCE_MISSING")
    if (row.get("identity_label") == "条码" and pattern == "qr_modules"
            or row.get("identity_label") == "二维码" and pattern == "linear_bars"
            or row.get("identity_label") == "条码" and static in {"二维码", "二维码粘贴区"}
            or row.get("identity_label") == "二维码" and static in {"条码", "条形码粘贴区"}):
        _flag(row, "MODEL_OBS_BARCODE_TYPE_CONFLICT")


def _review_hidden_role(row, evidence, observed_official, printed_is_official):
    # instruction/other 都会从编辑器隐藏；自报有作答或身份内容时不能借此隐藏真实题区。
    if (observed_official or printed_is_official or evidence["option_pattern"] in _OPTION_FEATURES
            or evidence["identity_pattern"] in _IDENTITY_FEATURES
            or evidence["barcode_pattern"] in _BARCODE_FEATURES):
        _flag(row, f"MODEL_OBS_{row['role'].upper()}_CONTENT_CONFLICT")
    if row["role"] == "instruction" and evidence["static_label"] not in _INSTRUCTION_LABELS:
        _flag(row, "MODEL_OBS_INSTRUCTION_EVIDENCE_MISSING")


def _answer_observed_labels(row, observed, official):
    parent = row.get("printed_question_label")
    if (row.get("role") != "answer" or parent not in official
            or re.fullmatch(r"[0-9]{1,3}", parent) is None):
        return observed
    # 正式独立小题优先；只有本框已明确指向父题，且所有观察都属于该父题时，才将连续小问视为同题。
    # 不改原观察，也不把跨父题、未知题号或选择题大区按数字前缀强行关联。
    child = re.compile(re.escape(parent) + r"(?:[.．-][0-9]{1,2}|\([0-9]{1,2}\)|（[0-9]{1,2}）)")
    if observed and all(label == parent or label not in official and child.fullmatch(label) for label in observed):
        return {parent}
    return observed


def review_model_observations(row, payload):
    """标记观察中的矛盾；旧 schema 没有观察字段时不改变原流程。

    本函数不直接产生 supported/confirmed，一致结果由调用方交给自动采用规则。
    观察题号保留供后续诊断；公开候选是否携带它由调用方的脱敏映射决定。
    """
    if "observed_question_labels" not in row and "visual_evidence" not in row:
        return
    labels, evidence = row.get("observed_question_labels"), row.get("visual_evidence")
    if not _valid_observations(labels, evidence):
        _flag(row, "MODEL_OBSERVATIONS_INVALID")
        return
    _review_anchor(row, evidence["anchor_bbox"])
    official = set(payload.get("question_labels", []))
    objective = set(payload.get("objective_question_labels", []))
    fill = set(payload.get("fill_blank_question_labels", []))
    observed = _answer_observed_labels(row, set(labels), official)
    observed_official = observed & official
    if observed - official:
        _flag(row, "MODEL_OBS_QUESTION_LABEL_NOT_IN_LIST")
    role = row.get("role")
    static = _STATIC_LABEL_CATEGORIES.get(evidence["static_label"], evidence["static_label"])
    if static in _INSTRUCTION_LABELS and role in {"identity", "answer", "objective_group"}:
        _flag(row, "MODEL_OBS_WARNING_CATEGORY_CONFLICT")
    if role == "objective_group":
        if "objective_question_labels" in payload and observed_official - objective:
            _flag(row, "MODEL_OBS_OBJECTIVE_QUESTION_TYPE_CONFLICT")
        if evidence["option_pattern"] not in _OPTION_FEATURES:
            _flag(row, "MODEL_OBS_OBJECTIVE_PATTERN_MISSING")
        if not observed:
            _flag(row, "MODEL_OBS_OBJECTIVE_LABELS_MISSING")
        if static in _IDENTITY_LABELS | _BARCODE_LABELS | {"填空题", "解答题"}:
            _flag(row, "MODEL_OBS_OBJECTIVE_STATIC_LABEL_CONFLICT")
        if evidence["identity_pattern"] in _IDENTITY_FEATURES:
            _flag(row, "MODEL_OBS_OBJECTIVE_IDENTITY_CONFLICT")
        if evidence["barcode_pattern"] in _BARCODE_FEATURES:
            _flag(row, "MODEL_OBS_OBJECTIVE_BARCODE_CONFLICT")
    elif role == "identity":
        _review_identity(row, evidence, observed_official)
    elif role in {"instruction", "other"}:
        _review_hidden_role(row, evidence, observed_official, row.get("printed_question_label") in official)
    elif role == "answer":
        associated = {row["printed_question_label"]} if row.get("printed_question_label") in official else set()
        observed_or_associated = observed_official | associated
        # 单题关联与群组观察不能互相替代；保留冲突而不选择其中一个题号或自动拆/删大框。
        if associated and observed and not associated <= observed:
            _flag(row, "MODEL_OBS_ANSWER_LABEL_CONFLICT")
        if len(observed_official) > 1:
            _flag(row, "MODEL_OBS_MULTIPLE_QUESTION_LABELS")
        if static in _IDENTITY_LABELS | _BARCODE_LABELS:
            _flag(row, "MODEL_OBS_ANSWER_STATIC_LABEL_CONFLICT")
        if evidence["identity_pattern"] in _IDENTITY_FEATURES:
            _flag(row, "MODEL_OBS_ANSWER_IDENTITY_CONFLICT")
        if evidence["barcode_pattern"] in _BARCODE_FEATURES:
            _flag(row, "MODEL_OBS_ANSWER_BARCODE_CONFLICT")
        if (static == "填空题" and "fill_blank_question_labels" in payload
                and observed_or_associated - fill):
            _flag(row, "MODEL_OBS_FILL_QUESTION_TYPE_CONFLICT")
        if observed_or_associated & fill and evidence["option_pattern"] in _OPTION_FEATURES:
            _flag(row, "MODEL_OBS_FILL_OPTION_CONFLICT")
        if (static == "选择题" and "objective_question_labels" in payload
                and observed_or_associated - objective):
            _flag(row, "MODEL_OBS_QUESTION_TYPE_CONFLICT")
