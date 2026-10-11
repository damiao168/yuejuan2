import pytest
import sympy

from math_verification_worker.parser import UnsupportedExpression, parse_restricted_latex
from math_verification_worker.verifier import VerificationError, ast_to_sympy


@pytest.mark.parametrize("command", [r"\leq", r"\geq"])
def test_command_prefix_is_matched_before_following_identifier(command):
    ast = parse_restricted_latex("x" + command + "x")
    assert ast["kind"] == "inequality"
    assert ast["value"] == command
    assert ast["children"][1] == {"kind": "symbol", "value": "x"}


def test_unicode_decimal_digits_and_whitespace_keep_their_original_values():
    ast = parse_restricted_latex("x\u00a0+\u0661.\u0662")
    assert ast["children"][1] == {"kind": "number", "value": "\u0661.\u0662"}


def test_non_ascii_identifier_remains_unsupported():
    with pytest.raises(UnsupportedExpression, match="^unsupported construct$"):
        parse_restricted_latex("\u03b1+1")


# 同一超限输入分别含有和不含非法残留，保护词法错误先于复杂度错误的顺序。
@pytest.mark.parametrize(
    ("suffix", "message"),
    [(";", "unsupported construct"), ("", "expression is too complex")],
)
def test_invalid_residue_is_reported_before_token_count_limit(suffix, message):
    with pytest.raises(UnsupportedExpression, match=f"^{message}$"):
        parse_restricted_latex("1+" * 512 + "1" + suffix)


def test_ast_child_error_is_reported_before_unknown_parent_kind():
    with pytest.raises(VerificationError, match="^AST node must be an object$"):
        ast_to_sympy({"kind": "unknown", "children": [None]}, {})


def test_fraction_with_one_child_keeps_the_original_arity_error():
    with pytest.raises(VerificationError, match="^unsupported AST kind: fraction$"):
        ast_to_sympy({"kind": "fraction", "children": [{"kind": "number", "value": "1"}]}, {})


def test_decimal_equation_keeps_exact_values_and_unevaluated_equality():
    expression = ast_to_sympy(parse_restricted_latex("0.1+0.2=0.3"), {})
    assert isinstance(expression, sympy.Equality)
    assert expression.lhs == expression.rhs == sympy.Rational(3, 10)
