import re
import unicodedata

from .prompt_injection_rules_generated import INJECTION_RULES

_ZERO_WIDTH = re.compile(r"[\u200B-\u200D\uFEFF]")
_SPACE = re.compile(r"\s+")

_INJECTION_RULES = tuple((rule["id"], re.compile(rule["pattern"], re.IGNORECASE), rule.get("ordered_terms", ())) for rule in INJECTION_RULES)


def normalize_evidence_text(value):
    # 引文和严格别名只容忍字宽、大小写与空白差异；删标点会把负数、分数或关系式改成另一个答案。
    text = unicodedata.normalize("NFKC", str(value or "")).casefold()
    return _SPACE.sub("", text)


def _contains_ordered_terms(text, terms):
    # 固定词组只需按顺序出现；不用连续通配符，避免学生长文本触发回溯。
    if not terms:
        return False
    position = 0
    for term in terms:
        position = text.find(term, position)
        if position < 0:
            return False
        position += len(term)
    return True


# 规则命中只提供风险信号；未命中不代表学生文本可信，后续仍按不可信证据处理。
def detect_prompt_injection(value):
    text = unicodedata.normalize("NFKC", str(value or ""))
    text = _ZERO_WIDTH.sub("", text).casefold()
    text = _SPACE.sub("", text)
    matches = [
        name
        for name, pattern, terms in _INJECTION_RULES
        if pattern.search(text) or _contains_ordered_terms(text, terms)
    ]
    return {"detected": bool(matches), "signals": matches}
