import math

import pytest
from ocr_worker.formula_validation import (
    _unit,
    normalize_latex,
    validate_latex_structure,
)


@pytest.mark.parametrize("gap", ["", " ", "   ", "\t", "\n", "\u2003", " \t\n "])
def test_normalizer_keeps_whitespace_and_adjacent_punctuation(gap: str) -> None:
    assert normalize_latex(f"x{gap}+{gap}({gap}y{gap}-{gap}1{gap}){gap}&{gap}z") == "x+(y-1)&z"
    assert normalize_latex(f"\\tfrac{gap}{{{gap}1{gap}}}{gap}{{{gap}2{gap}}}") == r"\frac{1}{2}"
    assert normalize_latex(f"\\dfrac{gap}{{1}}{{2}}") == r"\frac{1}{2}"


@pytest.mark.parametrize(
    ("latex", "expected"),
    [
        (r"\{x\}", (True, True, ())),
        ("x]", (False, False, ("unbalanced_delimiter",))),
        (r"\begin{cases}x\end{matrix}", (False, False, ("environment_mismatch",))),
        (r"\begin{bad}x\end{bad}", (True, False, ("unsupported_environment",))),
        (r"\input{secret}", (True, False, ("forbidden_macro", "unknown_macro"))),
        ("x^+1", (True, False, ("missing_script_operand",))),
        (r"x\in\text{实数}", (True, True, ())),
        ("x+中文", (True, False, ("formula_contains_prose",))),
        (r"\frac{x=1}{y=2}", (True, False, ("fraction_contains_relation",))),
        ("{}", (True, False, ("missing_math_content",))),
    ],
)
def test_structure_helpers_keep_reason_order_and_deduplication(latex: str, expected: tuple) -> None:
    assert validate_latex_structure(latex) == expected


def test_structure_gate_keeps_early_length_and_control_checks() -> None:
    assert validate_latex_structure("") == (False, False, ("empty_latex",))
    assert validate_latex_structure("x" * 12_001) == (False, False, ("latex_too_long",))
    assert validate_latex_structure("x\x00") == (False, False, ("latex_control_character",))


def test_structure_gate_keeps_multiple_failures_in_original_check_order() -> None:
    assert validate_latex_structure(r"[\end{cases}\input{secret}中文^+") == (
        False,
        False,
        (
            "unbalanced_delimiter", "environment_mismatch", "forbidden_macro", "unknown_macro",
            "missing_script_operand", "formula_contains_prose",
        ),
    )


def test_unit_keeps_empty_zero_and_nonzero_vector_shape() -> None:
    assert _unit([]) == []
    assert _unit([0, 0, 0]) == [0, 0, 0]
    normalized = _unit([3, 4])
    assert normalized == [0.6, 0.8]
    assert math.isclose(sum(value * value for value in normalized), 1)
