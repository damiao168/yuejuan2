"""局部图像证据与正式题型交叉检查；印刷框只证明边界，不证明类别。

OCR 一页最多执行一次，选项检查只读取有限裁区。没有检测到内容属于证据不足，
不能反推页面不存在该字段；多模态结果与正式题型一致即可自动采用，教师可纠错。
"""

import csv
import io
import math
import re
import shutil
import subprocess
from collections import Counter
from itertools import pairwise

import cv2

from .choice_grid_detection import detect_option_groups

SEMANTIC_VERSION = "multimodal-primary-local-content-evidence-v3"
_LABELS = {
    "姓名": ("姓名", "name"), "学号": ("学号", "studentid", "studentno"),
    "准考证号": ("准考证号", "准考证", "candidateno", "candidateid"),
    "考号": ("考号", "examno", "examinationno"), "班级": ("班级", "class"),
    "学校": ("学校", "school"), "条码": ("条码", "条形码", "barcode", "贴码"),
    "二维码": ("二维码", "qrcode"),
}
# OCR 常把“请勿在此处作答”拆成多个词，先做紧凑化，再同时检查常见动作词。
# 只在候选框内部执行这个反证；框外的说明不能撤回相邻的真实作答区。
_FORBIDDEN = (
    "禁止作答", "禁止答题", "禁止填写", "禁止书写",
    "请勿作答", "请勿答题", "请勿填写", "请勿书写",
    "不得作答", "不得答题", "不得填写", "不得书写",
    "不能作答", "不能答题", "不能填写", "不能书写",
    "切勿作答", "切勿答题", "切勿填写", "切勿书写",
    "非作答区", "非答题区", "非作答区域", "非答题区域", "作答区外", "注意事项",
    "禁止涂写", "请勿涂写", "不得涂写", "禁填", "勿作答", "勿答题", "勿填写",
    "donotwrite", "donotanswer", "donotfill",
)
_FORBIDDEN_PREFIXES = ("禁止", "请勿", "不得", "不能", "切勿")
_FORBIDDEN_ACTIONS = ("作答", "答题", "填写", "书写")
_OCR_LONG_EDGE = 2000
_OCR_MAX_OUTPUT_BYTES = 4 * 1024 * 1024
_MAX_CHECKED_REGIONS = 64
_CHOICE_HEADING = re.compile(
    r"^(?:[一二三四五六七八九十百0-9]+[、.．)）:：]?)?"
    r"(?:单项选择题|多项选择题|单选题|多选题|选择题)(?:[（(][^）)]*[）)])?$|^multiplechoice(?:questions?)?$"
)


def _identity_labels(tokens):
    # 只匹配字段词本身或贴码区说明，不能把 surname、学生填写值或正文中的 name 当标签。
    labels = set()
    for token in tokens:
        text = _compact(token["text"])
        for label, words in _LABELS.items():
            if any(text == word or (label in {"条码", "二维码"} and text in {
                word + suffix for suffix in ("粘贴区", "粘贴处", "贴附区", "区", "位置")
            }) for word in words):
                labels.add(label)
    return labels


def _question_marker(tokens, bbox, label):
    if not label:
        return False
    # 正文中也可能出现 (15) 或 15.5；题号须位于作答区起始带，且标点后不能紧接数学小数。
    pattern = re.compile(rf"^(?:第\s*{re.escape(label)}\s*题|[（(]\s*{re.escape(label)}\s*[）)]|{re.escape(label)}\s*(?:题|[)、.]))(?!\d)")
    for token in tokens:
        if (token["x"] <= bbox["x"] + bbox["width"] * .35
                and token["y"] <= bbox["y"] + min(.035, bbox["height"] * .15)
                and pattern.match(token["text"].strip())):
            return True
    return False


def _compact(text):
    return re.sub(r"[\s:：_\-]+", "", text).lower()


def _contains_forbidden_marker(text):
    """识别带插入说明词的禁答句，避免 OCR 分词绕过禁区反证。"""
    compact = _compact(text)
    if any(marker in compact for marker in _FORBIDDEN):
        return True
    # 例如“请勿在此处作答”“不得在框内填写”；动作词必须同时出现，
    # 不能仅凭“请勿”或“禁止”把普通提示误判为禁区。
    return any(prefix in compact and action in compact
               for prefix in _FORBIDDEN_PREFIXES for action in _FORBIDDEN_ACTIONS)


def _contains_forbidden_tokens(tokens):
    """同时检查同一行相邻 OCR 词，避免分块结果拆散禁答短句。"""
    ordered = sorted((token for token in tokens if token.get("text")),
                     key=lambda token: (token["y"], token["x"]))
    for index, token in enumerate(ordered):
        if _contains_forbidden_marker(token["text"]):
            return True
        text = token["text"]
        right = token["x"] + token["width"]
        height = token["height"]
        for following in ordered[index + 1:]:
            # 只串联同一行、间距接近字高的词；跨行或跨列不能制造禁答证据。
            if abs(following["y"] - token["y"]) > max(height, following["height"]) * .75:
                if following["y"] > token["y"]:
                    break
                continue
            gap = following["x"] - right
            if gap < -max(height, following["height"]) * .5 or gap > max(height, following["height"]) * 1.8:
                if following["x"] > right:
                    break
                continue
            text += following["text"]
            if _contains_forbidden_marker(text):
                return True
            right = max(right, following["x"] + following["width"])
    return False


def _independent_choice_content(tokens, groups):
    if not groups:
        return False
    # 编号格也可能命中选项形状；只有框内真实标题或重复选项字母才足以反驳身份类别。
    # OCR 为拼中文标签追加的组合词不能重复计数，否则一组 A/B/C/D 会被拼接放大成多组。
    letters = Counter(letter for token in tokens if not token.get("_joined_static_words")
                      for letter in re.findall(r"(?<![a-z0-9])[a-h](?![a-z0-9])", token["text"].lower()))
    repeated_letters = sum(count >= 2 for count in letters.values()) >= 3
    heading = any(_CHOICE_HEADING.fullmatch(_compact(token["text"])) for token in tokens)
    return repeated_letters or heading


def _reason(row, code):
    if code not in row["reason_codes"]:
        row["reason_codes"].append(code)


def review_raw_consistency(row, payload):
    """清理前保留矛盾原因；只保留正式白名单题号，防止学生填写值进入模板。"""
    role, label = row["role"], row["printed_question_label"]
    if role in {"identity", "objective_group"}:
        _reason(row, f"MODEL_PROPOSED_{role.upper()}")
    official = set(payload["question_labels"])
    objective = set(payload.get("objective_question_labels", []))
    if role == "objective_group" and label in official and label not in objective:
        _reason(row, "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT")
    if role == "identity" and label in official:
        _reason(row, "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT")
    if role != "identity" and row.get("identity_label") is not None:
        _reason(row, "SEMANTIC_ROLE_IDENTITY_CONFLICT")
    if role != "answer" and row["association_status"] != "unassociated":
        _reason(row, "SEMANTIC_ASSOCIATION_CONFLICT")
    if any(code.startswith("SEMANTIC_") and code.endswith("CONFLICT") for code in row["reason_codes"]):
        row["semantic_check"] = "needs_review"
        row["role"] = "uncertain"
        row["association_status"] = "unassociated"
        row["identity_label"] = None
        row["printed_question_label"] = label if label in official else None


class LocalContentEvidence:
    def __init__(self, image, page, *, ocr=None, barcode=None):
        self.image, self.page = image, page
        self._ocr = ocr or read_static_ocr
        self._barcode = barcode or detect_barcode
        self._tokens = None
        self._ocr_reason = None
        self._checks = 0
        self._option_cache = {}

    def static_tokens(self):
        """仅供同次识别的补漏模块复用内存 OCR，不再次运行或向诊断输出填写文本。"""
        return self._region_tokens({"x": 0, "y": 0, "width": 1, "height": 1}, context=False)

    def review(self, rows, payload):
        """多模态负责识别，局部证据补充或否决；OCR 漏字不把有效题号一律变成待关联。"""
        for row in rows:
            if row.get("semantic_check") == "needs_review":
                continue
            role = row["role"]
            automatic = _multimodal_usable(row, payload)
            row["semantic_check"] = "not_evaluated"
            if role not in {"answer", "identity", "objective_group"}:
                if role == "uncertain":
                    row["semantic_check"] = "needs_review"
                continue
            if self._checks >= _MAX_CHECKED_REGIONS:
                self._insufficient(row, "SEMANTIC_CHECK_BUDGET_EXCEEDED", downgrade=role != "answer", automatic=automatic)
                continue
            self._checks += 1
            tokens = self._region_tokens(row["proposed_bbox"])
            texts = [token["text"] for token in tokens]
            compact = _compact(" ".join(texts))
            # 邻近上下文可用于字段标签，禁止作答反证只读框内文字，避免污染相邻的合法题区。
            inside = self._region_tokens(row["proposed_bbox"], context=False)
            if _contains_forbidden_tokens(inside):
                _reason(row, "SEMANTIC_FORBIDDEN_AREA_DETECTED")
                self._insufficient(row, "SEMANTIC_CONTENT_CONFLICT")
                continue
            groups = self._option_groups(row["proposed_bbox"])
            if role == "objective_group":
                # 编号填涂栏也有重复格；仅有格子形状不够，还需要真实选项字母或选择题标题。
                option_letters = len(set(re.findall(r"(?<![a-z])[a-h](?![a-z])", " ".join(texts).lower()))) >= 3
                choice_heading = any(word in compact for word in ("单选题", "多选题", "选择题", "multiplechoice"))
                if groups and (option_letters or choice_heading):
                    self._supported(row, "SEMANTIC_OPTION_PATTERN_VERIFIED")
                elif _identity_labels(inside):
                    self._insufficient(row, "SEMANTIC_CONTENT_CONFLICT")
                else:
                    self._insufficient(row, "SEMANTIC_EVIDENCE_INSUFFICIENT", automatic=automatic)
            elif role == "identity":
                label = row.get("identity_label") or "身份信息"
                if _independent_choice_content(inside, groups):
                    self._insufficient(row, "SEMANTIC_IDENTITY_OPTION_CONTENT_CONFLICT")
                    continue
                matching = _identity_labels(tokens)
                if label == "身份信息" and len(matching) == 1:
                    row["identity_label"] = label = next(iter(matching))
                if label in {"条码", "二维码"}:
                    code = self._barcode(self._crop(row["proposed_bbox"]))
                    if code == ("qr" if label == "二维码" else "barcode"):
                        self._supported(row, "SEMANTIC_BARCODE_DECODE_VERIFIED")
                    elif label in matching:
                        # 贴码区可以是空白；明确静态标签也是证据，不要求空白框能解码。
                        self._supported(row, "SEMANTIC_BLANK_BARCODE_AREA_VERIFIED")
                    else:
                        if code == "stripes":
                            _reason(row, "SEMANTIC_BARCODE_PATTERN_ONLY")
                        self._insufficient(row, "SEMANTIC_EVIDENCE_INSUFFICIENT", automatic=automatic)
                elif matching == {label} and not groups:
                    self._supported(row, "SEMANTIC_IDENTITY_LABEL_VERIFIED")
                else:
                    conflict = bool(matching and label not in matching)
                    self._insufficient(row, "SEMANTIC_CONTENT_CONFLICT" if conflict else "SEMANTIC_EVIDENCE_INSUFFICIENT",
                                       automatic=automatic and not conflict)
            else:
                label = row["printed_question_label"]
                # 数学过程中的孤立数字不是题号证据；只接受带题号标点或“题”的局部标记。
                visible = _question_marker(inside, row["proposed_bbox"], label)
                if (visible and label in payload["question_labels"]
                        and label not in payload.get("objective_question_labels", [])):
                    self._supported(row, "SEMANTIC_QUESTION_LABEL_VERIFIED")
                else:
                    self._insufficient(row, "SEMANTIC_EVIDENCE_INSUFFICIENT", downgrade=False, automatic=automatic)
        # 独立内容证据仍不能消除区域相互覆盖的歧义；不能在几何冲突后重新显示为语义已核验。
        for row in rows:
            if "REGION_CONFLICT" in row["reason_codes"]:
                self._insufficient(row, "SEMANTIC_REGION_OVERLAP_CONFLICT")

    def _region_tokens(self, bbox, context=True):
        if self._tokens is None:
            try:
                self._tokens, self._ocr_reason = self._ocr(self.image)
            except (OSError, ValueError, cv2.error):
                self._tokens, self._ocr_reason = [], "SEMANTIC_OCR_FAILED"
        # 标签可能紧贴输入横线外侧；仅加少量相邻上下文，不把整个页眉或邻题当证据。
        margin_x = min(.015, bbox["width"] * .08) if context else 0
        margin_y = min(.015, bbox["height"] * .10) if context else 0
        left, top = bbox["x"] - margin_x, bbox["y"] - margin_y
        right, bottom = bbox["x"] + bbox["width"] + margin_x, bbox["y"] + bbox["height"] + margin_y
        return [token for token in self._tokens if left <= token["x"] + token["width"] / 2 <= right
                and top <= token["y"] + token["height"] / 2 <= bottom]

    def _crop(self, bbox):
        height, width = self.image.shape[:2]
        left, top = max(0, math.floor(bbox["x"] * width)), max(0, math.floor(bbox["y"] * height))
        right = min(width, math.ceil((bbox["x"] + bbox["width"]) * width))
        bottom = min(height, math.ceil((bbox["y"] + bbox["height"]) * height))
        return self.image[top:bottom, left:right]

    def _option_groups(self, bbox):
        key = tuple(bbox[name] for name in ("x", "y", "width", "height"))
        if key not in self._option_cache:
            crop = self._crop(bbox)
            # 不知道选项数时只检查常见 4/5 格，未命中不能作为“不是选择题”的结论。
            groups = []
            for count in (4, 5):
                measured, _ = detect_option_groups(crop, count, "horizontal")
                groups.extend(measured)
                if measured:
                    break
            self._option_cache[key] = groups
        return self._option_cache[key]

    def _supported(self, row, code):
        row["semantic_check"] = "supported"
        _reason(row, code)
        ref = f"local-semantic:{SEMANTIC_VERSION}:{self.page['sha256'][:16]}:{self.page['page_no']}"
        row["evidence_refs"] = list(dict.fromkeys([*row["evidence_refs"], ref]))

    def _insufficient(self, row, code, downgrade=True, automatic=False):
        if automatic:
            # supported 表示可自动采用，不表示 OCR 独立通过；保留模型来源与 OCR 运行诊断。
            row["semantic_check"] = "supported"
            _reason(row, "MULTIMODAL_CONTENT_CONSISTENT")
            if self._ocr_reason:
                _reason(row, self._ocr_reason)
            return
        row["semantic_check"] = "needs_review"
        _reason(row, code)
        if self._ocr_reason:
            _reason(row, self._ocr_reason)
        if downgrade:
            row["role"] = "uncertain"
            row["identity_label"] = None
            row["association_status"] = "unassociated"


def _multimodal_usable(row, payload):
    if "MULTIMODAL_OBSERVATIONS_CONSISTENT" not in row["reason_codes"]:
        return False
    if row["role"] == "answer":
        label = row["printed_question_label"]
        return (row["association_status"] == "associated" and label in payload["question_labels"]
                and label not in payload.get("objective_question_labels", []))
    return row["role"] in {"identity", "objective_group"}


def read_static_ocr(image):
    """返回仅供本次内存核验的 OCR token；调用方不能记录这些填写文本。"""
    executable = shutil.which("tesseract")
    if executable is None:
        return [], "SEMANTIC_OCR_UNAVAILABLE"
    height, width = image.shape[:2]
    scale = min(1, _OCR_LONG_EDGE / max(width, height))
    resized = cv2.resize(image, (round(width * scale), round(height * scale))) if scale < 1 else image
    data = cv2.imencode(".png", cv2.cvtColor(resized, cv2.COLOR_RGB2BGR))[1].tobytes()
    try:
        result = subprocess.run([executable, "stdin", "stdout", "-l", "chi_sim+eng", "--psm", "11", "tsv"],
                                input=data, capture_output=True, timeout=12, check=False)
    except subprocess.TimeoutExpired:
        return [], "SEMANTIC_OCR_TIMEOUT"
    except OSError:
        return [], "SEMANTIC_OCR_PROCESS_FAILED"
    if result.returncode != 0:
        return [], "SEMANTIC_OCR_PROCESS_FAILED"
    if len(result.stdout) > _OCR_MAX_OUTPUT_BYTES:
        return [], "SEMANTIC_OCR_OUTPUT_TOO_LARGE"
    tokens, lines = [], {}
    try:
        # Tesseract 的 TSV 不转义引号；数学内容中的双引号必须按原字保留，不能吞掉后续行。
        rows = csv.DictReader(io.StringIO(result.stdout.decode("utf-8")), delimiter="\t", quoting=csv.QUOTE_NONE)
        required = {"level", "block_num", "par_num", "line_num", "left", "top", "width", "height", "conf", "text"}
        if rows.fieldnames is None or not required <= set(rows.fieldnames):
            return [], "SEMANTIC_OCR_PARSE_FAILED"
        for row in rows:
            if None in row or any(row[key] is None for key in required):
                return [], "SEMANTIC_OCR_PARSE_FAILED"
            if row["level"] != "5" or float(row["conf"]) < 45 or not row["text"].strip():
                continue
            token = {"text": row["text"], "x": int(row["left"]) / resized.shape[1],
                     "y": int(row["top"]) / resized.shape[0], "width": int(row["width"]) / resized.shape[1],
                     "height": int(row["height"]) / resized.shape[0]}
            tokens.append(token)
            lines.setdefault(tuple(row[key] for key in ("block_num", "par_num", "line_num")), []).append(token)
    except (csv.Error, KeyError, TypeError, ValueError):
        # 只回传稳定错误码；引擎输出和异常可能含学生填写内容，不能用于普通日志或界面。
        return [], "SEMANTIC_OCR_PARSE_FAILED"
    # 中文标签有时拆词；仅拼接间距接近字高的同一行邻词，不能跨列把正文拼成字段标签。
    for words in lines.values():
        for index in range(len(words)):
            for length in (2, 3, 4):
                part = words[index:index + length]
                if len(part) != length:
                    continue
                if any(right["x"] - left["x"] - left["width"] > max(left["height"], right["height"]) * 1.5
                       for left, right in pairwise(part)):
                    continue
                left, top = min(word["x"] for word in part), min(word["y"] for word in part)
                tokens.append({"text": "".join(word["text"] for word in part), "x": left, "y": top,
                               "width": max(word["x"] + word["width"] for word in part) - left,
                               "height": max(word["y"] + word["height"] for word in part) - top,
                               "_joined_static_words": True})
    return tokens, None


def detect_barcode(image):
    """只返回类别信号，不返回条码中编码的个人或答卷标识。"""
    if image.size == 0:
        return None
    try:
        decoded, _, _ = cv2.QRCodeDetector().detectAndDecode(image)
        if decoded:
            return "qr"
        detector = getattr(cv2, "barcode_BarcodeDetector", None)
        if detector is not None:
            result = detector().detectAndDecode(image)
            decoded = result[0] if result else None
            if isinstance(decoded, str) and decoded or isinstance(decoded, (tuple, list)) and any(decoded):
                return "barcode"
    except (cv2.error, TypeError):
        pass
    gray = cv2.cvtColor(image, cv2.COLOR_RGB2GRAY)
    ink = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    # 条纹仅是辅助信号；文字竖笔也可能命中，不能凭这一项自动认定为条码。
    vertical = cv2.morphologyEx(ink, cv2.MORPH_OPEN, cv2.getStructuringElement(cv2.MORPH_RECT, (1, max(12, image.shape[0] // 3))))
    contours, _ = cv2.findContours(vertical, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
    stripes = [cv2.boundingRect(contour) for contour in contours]
    if sum(height >= max(12, image.shape[0] * .3) and height >= width * 3 for _, _, width, height in stripes) >= 12:
        return "stripes"
    return None
