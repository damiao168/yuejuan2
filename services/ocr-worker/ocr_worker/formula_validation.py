from __future__ import annotations

import io
import math
import re
import unicodedata
from collections.abc import Callable
from dataclasses import dataclass
from enum import StrEnum


class FormulaAction(StrEnum):
    ACCEPT = "accept"
    RECROP = "recrop"
    RETRY_L = "retry_l"
    REVIEW = "review"


@dataclass(frozen=True)
class FormulaValidationResult:
    syntax_valid: bool
    structure_valid: bool
    render_valid: bool | None
    crop_complete: bool
    render_similarity: float | None
    action: FormulaAction
    reason_codes: tuple[str, ...] = ()

    @property
    def valid(self) -> bool:
        return self.action is FormulaAction.ACCEPT


_UNICODE_MATH = str.maketrans(
    {
        "×": r"\times ",
        "÷": r"\div ",
        "±": r"\pm ",
        "∓": r"\mp ",
        "≤": r"\leq ",
        "≥": r"\geq ",
        "≠": r"\neq ",
        "≈": r"\approx ",
        "∞": r"\infty ",
        "√": r"\sqrt ",
        "∑": r"\sum ",
        "∏": r"\prod ",
        "∫": r"\int ",
        "∈": r"\in ",
        "∉": r"\notin ",
        "⊂": r"\subset ",
        "⊆": r"\subseteq ",
        "∠": r"\angle ",
        "⊥": r"\perp ",
        "∥": r"\parallel ",
        "π": r"\pi ",
        "α": r"\alpha ",
        "β": r"\beta ",
        "γ": r"\gamma ",
        "θ": r"\theta ",
        "λ": r"\lambda ",
        "μ": r"\mu ",
        "σ": r"\sigma ",
        "ω": r"\omega ",
    }
)
_SUPERSCRIPTS = str.maketrans("⁰¹²³⁴⁵⁶⁷⁸⁹", "0123456789")
_SUBSCRIPTS = str.maketrans("₀₁₂₃₄₅₆₇₈₉", "0123456789")

# This is intentionally a broad allow-list for printed middle-school and
# high-school mathematics. Unknown macros fail closed instead of reaching a
# renderer that may interpret file or process commands.
_ALLOWED_MACROS = {
    "alpha", "beta", "gamma", "delta", "epsilon", "varepsilon", "zeta", "eta", "theta",
    "vartheta", "iota", "kappa", "lambda", "mu", "nu", "xi", "pi", "varpi", "rho",
    "varrho", "sigma", "varsigma", "tau", "upsilon", "phi", "varphi", "chi", "psi", "omega",
    "Gamma", "Delta", "Theta", "Lambda", "Xi", "Pi", "Sigma", "Upsilon", "Phi", "Psi", "Omega",
    "frac", "sqrt", "overline", "underline", "widehat", "widetilde", "hat", "bar", "vec", "dot", "ddot",
    "overrightarrow", "overleftarrow", "overbrace", "underbrace", "overset", "underset", "stackrel",
    "sin", "cos", "tan", "cot", "sec", "csc", "arcsin", "arccos", "arctan", "sinh", "cosh", "tanh",
    "log", "ln", "lg", "lim", "max", "min", "sup", "inf", "det", "gcd", "Pr", "exp",
    "sum", "prod", "coprod", "int", "iint", "iiint", "oint", "partial", "nabla", "infty",
    "times", "div", "cdot", "ast", "star", "circ", "bullet", "oplus", "otimes", "pm", "mp",
    "le", "leq", "ge", "geq", "ne", "neq", "approx", "sim", "simeq", "equiv", "propto",
    "in", "notin", "ni", "subset", "supset", "subseteq", "supseteq", "cup", "cap", "emptyset",
    "forall", "exists", "therefore", "because", "angle", "triangle", "perp", "parallel", "cong",
    "left", "right", "middle", "lvert", "rvert", "lVert", "rVert", "vert", "Vert",
    "langle", "rangle", "lfloor", "rfloor", "lceil", "rceil", "{", "}", "|",
    "begin", "end", "\\", "quad", "qquad", ",", ";", ":", "!", " ",
    "text", "textrm", "mathrm", "mathbf", "mathit", "mathsf", "mathtt", "mathcal", "mathbb",
    "operatorname", "rm", "bf", "it", "displaystyle", "textstyle", "scriptstyle", "scriptscriptstyle",
    "limits", "nolimits", "substack", "pmod", "mod", "bmod", "colon", "mid", "not", "complement",
    "ell", "Re", "Im",
    "color", "boxed", "cancel", "degree", "prime", "ldots", "cdots", "vdots", "ddots",
    "to", "rightarrow", "leftarrow", "Rightarrow", "Leftarrow", "leftrightarrow", "Leftrightarrow",
}
_ALLOWED_ENVIRONMENTS = {
    "array", "matrix", "pmatrix", "bmatrix", "Bmatrix", "vmatrix", "Vmatrix",
    "cases", "aligned", "alignedat", "gathered", "split",
}
_FORBIDDEN_MACROS = {
    "input", "include", "includegraphics", "write", "openout", "read", "catcode", "csname",
    "newcommand", "renewcommand", "def", "edef", "gdef", "xdef", "usepackage", "documentclass",
    "href", "url", "htmlClass", "htmlId", "htmlStyle",
}


def normalize_latex(value: str) -> str:
    """Create a stable, safe comparison form without changing mathematical meaning."""
    text = str(value or "").strip()
    if not text:
        return ""
    text = _replace_script_digits(text, superscript=True)
    text = _replace_script_digits(text, superscript=False)
    text = unicodedata.normalize("NFKC", text)
    text = _strip_outer_math_delimiters(text).translate(_UNICODE_MATH)
    # FormulaNet occasionally serializes a command with two leading slashes.
    # Keep LaTeX row separators intact, but collapse duplicates before letters.
    text = re.sub(r"\\\\(?=[A-Za-z])", r"\\", text)
    text = re.sub(r"\\(?:t|d)frac\b", r"\\frac", text)
    # LaTeX uses a dot after left/right to mean an invisible delimiter, not a
    # decimal point. Dropping only the command adds spurious dots to sets.
    text = re.sub(r"\\(?:left|right)\s*\.", "", text)
    text = re.sub(r"\\(?:left|right)\s*", "", text)
    text = re.sub(r"\s+", " ", text)
    text = re.sub(r"\\([A-Za-z]+)\s+\{", r"\\\1{", text)
    text = re.sub(r"\s*([{}_^=+\-*/(),\[\]])\s*", r"\1", text)
    text = re.sub(r"([_^])([A-Za-z0-9])", r"\1{\2}", text)
    text = re.sub(r"\s*(&)\s*", r"\1", text)
    return text.strip()


def validate_latex_structure(value: str) -> tuple[bool, bool, tuple[str, ...]]:
    latex = normalize_latex(value)
    reasons: list[str] = []
    if not latex:
        return False, False, ("empty_latex",)
    if len(latex) > 12_000:
        return False, False, ("latex_too_long",)
    if any(ord(char) < 32 and char not in "\n\t" for char in latex):
        return False, False, ("latex_control_character",)

    stack: list[str] = []
    pairs = {"{": "}", "[": "]", "(": ")"}
    escaped = False
    for char in latex:
        if escaped:
            escaped = False
            continue
        if char == "\\":
            escaped = True
            continue
        if char in pairs:
            stack.append(pairs[char])
        elif char in pairs.values() and (not stack or stack.pop() != char):
            reasons.append("unbalanced_delimiter")
            break
    if stack and "unbalanced_delimiter" not in reasons:
        reasons.append("unbalanced_delimiter")

    environment_stack: list[str] = []
    for match in re.finditer(r"\\(begin|end)\s*\{([^{}]+)\}", latex):
        operation, environment = match.groups()
        if environment not in _ALLOWED_ENVIRONMENTS:
            reasons.append("unsupported_environment")
            continue
        if operation == "begin":
            environment_stack.append(environment)
        elif not environment_stack or environment_stack.pop() != environment:
            reasons.append("environment_mismatch")
    if environment_stack:
        reasons.append("environment_mismatch")

    macros = re.findall(r"\\([A-Za-z]+|\\|[{}|,;:! ])", latex)
    if any(macro in _FORBIDDEN_MACROS for macro in macros):
        reasons.append("forbidden_macro")
    if any(macro not in _ALLOWED_MACROS for macro in macros):
        reasons.append("unknown_macro")
    if re.search(r"(?:\^|_)\s*(?:$|[=+\-*/&])", latex):
        reasons.append("missing_script_operand")
    # Formula layout occasionally selects the tail of a surrounding Chinese
    # sentence. FormulaNet can reproduce that prose and the render-back check
    # may still look similar, so syntax + pixels alone are not proof that this
    # is a mathematical expression. Chinese labels are allowed only when the
    # model explicitly places them inside a text-style LaTeX command.
    prose_check = re.sub(
        r"\\(?:text|textrm|mathrm)\s*\{[^{}]*\}", "", latex
    )
    if re.search(r"[\u4e00-\u9fff]", prose_check):
        reasons.append("formula_contains_prose")
    for match in re.finditer(r"\\frac\b", latex):
        argument_start = match.end()
        for _ in range(2):
            argument = _braced_argument(latex, argument_start)
            if argument is None:
                break
            content, argument_start = argument
            if _contains_top_level_relation(content):
                reasons.append("fraction_contains_relation")
    if not re.search(r"[A-Za-z0-9\u4e00-\u9fff]|\\(?:frac|sqrt|sum|int|pi|theta|angle)", latex):
        reasons.append("missing_math_content")

    syntax_valid = not any(
        reason in reasons
        for reason in ("unbalanced_delimiter", "environment_mismatch", "latex_control_character", "latex_too_long")
    )
    structure_valid = syntax_valid and not reasons
    return syntax_valid, structure_valid, tuple(dict.fromkeys(reasons))


def _braced_argument(latex: str, start: int) -> tuple[str, int] | None:
    while start < len(latex) and latex[start].isspace():
        start += 1
    if start >= len(latex) or latex[start] != "{":
        return None
    depth, escaped = 1, False
    for index in range(start + 1, len(latex)):
        char = latex[index]
        if escaped:
            escaped = False
        elif char == "\\":
            escaped = True
        elif char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return latex[start + 1:index], index + 1
    return None


def _contains_top_level_relation(latex: str) -> bool:
    depth = 0
    index = 0
    while index < len(latex):
        char = latex[index]
        if char == "\\":
            command = re.match(r"\\([A-Za-z]+|.)", latex[index:])
            if command:
                if depth == 0 and command.group(1) in {"le", "leq", "ge", "geq", "ne", "neq", "approx", "equiv"}:
                    return True
                index += command.end()
                continue
        elif char in "{([":
            depth += 1
        elif char in "})]":
            depth = max(0, depth - 1)
        elif depth == 0 and char in "=<>":
            return True
        index += 1
    return False


class FormulaValidator:
    def __init__(
        self,
        *,
        render_similarity_threshold: float = 0.34,
        renderer: Callable[[str], bytes | None] | None = None,
    ) -> None:
        self.render_similarity_threshold = render_similarity_threshold
        self._renderer = renderer if renderer is not None else _render_with_mathtext

    def validate(
        self,
        latex: str,
        crop: bytes,
        *,
        detector_score: float,
        crop_complete: bool,
        detector_accept_score: float = 0.65,
        render_similarity_threshold: float | None = None,
    ) -> FormulaValidationResult:
        syntax_valid, structure_valid, structural_reasons = validate_latex_structure(latex)
        reasons = list(structural_reasons)
        if detector_score < detector_accept_score:
            reasons.append("low_detector_confidence")
            return FormulaValidationResult(
                syntax_valid, structure_valid, None, crop_complete, None, FormulaAction.REVIEW,
                tuple(dict.fromkeys(reasons)),
            )
        if not crop_complete:
            reasons.append("roi_incomplete_after_recrop")
            return FormulaValidationResult(
                syntax_valid, structure_valid, None, False, None, FormulaAction.RECROP,
                tuple(dict.fromkeys(reasons)),
            )
        if not structure_valid:
            return FormulaValidationResult(
                syntax_valid, False, None, True, None, FormulaAction.RETRY_L,
                tuple(dict.fromkeys(reasons)),
            )

        rendered: bytes | None
        try:
            rendered = self._renderer(normalize_latex(latex))
        except ModuleNotFoundError:
            rendered = None
            reasons.append("render_validator_unavailable")
            return FormulaValidationResult(True, True, None, True, None, FormulaAction.REVIEW, tuple(reasons))
        except Exception:  # noqa: BLE001 - renderer backends expose heterogeneous parse errors.
            rendered = b""
        if rendered is None:
            reasons.append("render_validator_unavailable")
            return FormulaValidationResult(True, True, None, True, None, FormulaAction.REVIEW, tuple(reasons))
        if not rendered:
            reasons.append("latex_render_failed")
            return FormulaValidationResult(True, True, False, True, None, FormulaAction.RETRY_L, tuple(reasons))

        # 回渲染只比较图像形态；通过阈值不代表公式数学正确或与原图语义等价。
        similarity = formula_image_similarity(crop, rendered)
        threshold = self.render_similarity_threshold if render_similarity_threshold is None else render_similarity_threshold
        render_valid = similarity >= threshold
        if not render_valid:
            reasons.append("render_mismatch")
        return FormulaValidationResult(
            True,
            True,
            render_valid,
            True,
            similarity,
            FormulaAction.ACCEPT if render_valid else FormulaAction.RETRY_L,
            tuple(dict.fromkeys(reasons)),
        )


def formula_image_similarity(source: bytes, rendered: bytes) -> float:
    """Compare normalized ink projections; robust to font weight and small shifts."""
    from PIL import Image, ImageFilter, ImageOps

    def vector(data: bytes) -> tuple[list[float], list[float], float]:
        with Image.open(io.BytesIO(data)) as image:
            if image.mode in {"RGBA", "LA"} or "transparency" in image.info:
                rgba = image.convert("RGBA")
                white = Image.new("RGBA", rgba.size, "white")
                white.alpha_composite(rgba)
                gray = ImageOps.autocontrast(white.convert("L"))
            else:
                gray = ImageOps.autocontrast(image.convert("L"))
            ink = gray.point(lambda pixel: 255 if pixel < 205 else 0)
            bounds = ink.getbbox()
            if bounds is None:
                return [0.0] * 96, [0.0] * 48, 0.0
            ink = ink.crop(bounds)
            aspect = ink.width / max(ink.height, 1)
            target_width = max(1, min(384, round(48 * aspect)))
            normalized = ink.resize((target_width, 48)).filter(ImageFilter.GaussianBlur(0.7))
            rows = [sum(normalized.getpixel((x, y)) for x in range(normalized.width)) for y in range(48)]
            columns_raw = [sum(normalized.getpixel((x, y)) for y in range(48)) for x in range(normalized.width)]
            columns = _resample(columns_raw, 96)
            return _unit(rows), _unit(columns), aspect

    source_rows, source_columns, source_aspect = vector(source)
    rendered_rows, rendered_columns, rendered_aspect = vector(rendered)
    if source_aspect <= 0 or rendered_aspect <= 0:
        return 0.0
    row_score = sum(a * b for a, b in zip(source_rows, rendered_rows, strict=True))
    column_score = sum(a * b for a, b in zip(source_columns, rendered_columns, strict=True))
    aspect_score = math.exp(-abs(math.log(source_aspect / rendered_aspect)))
    return max(0.0, min(1.0, 0.30 * row_score + 0.50 * column_score + 0.20 * aspect_score))


def _render_with_mathtext(latex: str) -> bytes | None:
    try:
        from matplotlib.mathtext import math_to_image
    except ImportError as exc:
        raise ModuleNotFoundError("matplotlib mathtext is unavailable") from exc
    output = io.BytesIO()
    math_to_image(f"${latex}$", output, dpi=180, format="png")
    return output.getvalue()


def _strip_outer_math_delimiters(value: str) -> str:
    pairs = (("$$", "$$"), (r"\[", r"\]"), (r"\(", r"\)"), ("$", "$"))
    result = value.strip()
    for left, right in pairs:
        if result.startswith(left) and result.endswith(right) and len(result) >= len(left) + len(right):
            return result[len(left) : -len(right)].strip()
    return result


def _replace_script_digits(value: str, *, superscript: bool) -> str:
    alphabet = "⁰¹²³⁴⁵⁶⁷⁸⁹" if superscript else "₀₁₂₃₄₅₆₇₈₉"
    table = _SUPERSCRIPTS if superscript else _SUBSCRIPTS
    marker = "^" if superscript else "_"
    return re.sub(
        f"([{re.escape(alphabet)}]+)",
        lambda match: marker + "{" + match.group(1).translate(table) + "}",
        value,
    )


def _resample(values: list[float], size: int) -> list[float]:
    if not values:
        return [0.0] * size
    if len(values) == 1:
        return [values[0]] * size
    result: list[float] = []
    for index in range(size):
        position = index * (len(values) - 1) / max(size - 1, 1)
        left = math.floor(position)
        right = min(len(values) - 1, left + 1)
        fraction = position - left
        result.append(values[left] * (1 - fraction) + values[right] * fraction)
    return result


def _unit(values: list[float]) -> list[float]:
    norm = math.sqrt(sum(value * value for value in values))
    return [value / norm for value in values] if norm else [0.0 for value in values]
