import pytest

from math_verification_worker.parser import parse_restricted_latex
from math_verification_worker.verifier import (
    VerificationError,
    normalize,
    solve,
    verify_equivalence,
    verify_transition,
)


def test_algebraic_equivalence_and_solution_completeness():
    expanded = parse_restricted_latex("x^2+x")
    factored = parse_restricted_latex("x(x+1)")
    assert verify_equivalence(expanded, factored).status == "verified"
    complete = parse_restricted_latex("x^2=4")
    incomplete = parse_restricted_latex("x=2")
    result = verify_transition(complete, incomplete)
    assert result.status == "contradicted"
    assert result.relation == "solutions_lost"


def test_domain_assumptions_prevent_unsafe_radical_simplification():
    radical = parse_restricted_latex(r"\sqrt{x^2}")
    plain = parse_restricted_latex("x")
    assert verify_equivalence(radical, plain, {"variables": {"x": "real"}}).status != "verified"
    assert verify_equivalence(radical, plain, {"variables": {"x": "positive_real"}}).status == "verified"


def test_cancellation_preserves_excluded_denominator_values():
    quotient = parse_restricted_latex(r"\frac{x}{x}")
    one = parse_restricted_latex("1")
    result = verify_equivalence(quotient, one)
    assert result.status == "contradicted"
    assert result.relation == "defined_domain_changed"
    equation = parse_restricted_latex(r"\frac{x^2}{x}=0")
    assert solve(equation, "x")["solution_set"] == "EmptySet"


def test_radical_domain_and_unsupported_constraints_fail_closed():
    equation = parse_restricted_latex(r"\sqrt{x-2}=0")
    assert solve(equation, "x")["solution_set"] == "{2}"
    result = verify_equivalence(equation, equation, {"constraints": ["x>3"]})
    assert result.status == "uncertain"
    assert solve(equation, "x", {"constraints": ["x>3"]})["status"] == "uncertain"


@pytest.mark.parametrize(
    ("left", "right"),
    [("0.1x+0.2x", "0.3x"), ("0.1+0.2", "0.3"), ("1.20x/0.4", "3x")],
)
def test_finite_decimals_are_exact(left, right):
    result = verify_equivalence(parse_restricted_latex(left), parse_restricted_latex(right))
    assert result.status == "verified"
    assert normalize(parse_restricted_latex("0.1x+0.2x"))["canonical"] == "3*x/10"


def test_distinct_decimals_are_not_rounded_into_equivalence():
    result = verify_equivalence(
        parse_restricted_latex("0.10000000000000001x"), parse_restricted_latex("0.1x")
    )
    assert result.status == "contradicted"
    assert result.relation == "numeric_counterexample"


@pytest.mark.parametrize(
    ("left", "right"),
    [
        ("1/0", "2/0"),
        (r"\sqrt{0-1}", r"2\sqrt{0-1}/2"),
        (r"\sqrt{0-1}*\sqrt{0-1}", "0-1"),
        (r"\sqrt{0-1}*\sqrt{0-1}+x", "x-1"),
        ("1/0=1/0", "2/0=2/0"),
        ("(1/0)^0", "1"),
        ("0*(1/0)", "0"),
    ],
)
def test_undefined_original_subexpressions_are_never_verified(left, right):
    result = verify_transition(parse_restricted_latex(left), parse_restricted_latex(right))
    assert result.status == "uncertain"
    assert result.relation == "expression_not_defined"


def test_normalization_and_solving_reject_hidden_nonreal_constants():
    ast = parse_restricted_latex(r"\sqrt{0-1}*\sqrt{0-1}+x=0")
    assert solve(ast, "x") == {"status": "uncertain", "reason_code": "expression_not_defined"}
    with pytest.raises(VerificationError, match="not defined over the reals"):
        normalize(ast)


@pytest.mark.parametrize(
    ("equation", "declaration", "expected"),
    [
        ("x^2=2", "integer", "EmptySet"),
        ("x^2=4", "integer", "{-2, 2}"),
        ("x^2=4", "positive_real", "{2}"),
        ("x=0", "positive_real", "EmptySet"),
        ("x+0.5=0", "integer", "EmptySet"),
        ("x^2=4", "real", "{-2, 2}"),
    ],
)
def test_solutions_stay_inside_the_declared_variable_domain(equation, declaration, expected):
    result = solve(parse_restricted_latex(equation), "x", {"variables": {"x": declaration}})
    assert result["status"] == "verified"
    assert result["solution_set"] == expected


def test_positive_domain_is_used_for_equations_and_original_denominators():
    domain = {"variables": {"x": "positive_real"}}
    result = verify_transition(parse_restricted_latex("x^2=4"), parse_restricted_latex("x=2"), domain)
    assert result.status == "verified"
    assert result.relation == "equivalent_transform"
    assert verify_equivalence(parse_restricted_latex("x/x"), parse_restricted_latex("1"), domain).status == "verified"


def test_integer_domain_is_used_for_equivalence_solution_sets():
    result = verify_equivalence(
        parse_restricted_latex("x^2=2"), parse_restricted_latex("x^2=3"), {"variables": {"x": "integer"}}
    )
    assert result.status == "verified"
    assert result.relation == "solution_set_equivalent"
    assert result.evidence == ("EmptySet",)


def test_counterexamples_respect_positive_variable_domain():
    result = verify_equivalence(
        parse_restricted_latex("x"), parse_restricted_latex("0"), {"variables": {"x": "positive_real"}}
    )
    assert result.status == "contradicted"
    assert result.evidence == ("x=1",)


# 同一原式在三个声明域下约束不同；防止化简后把非实根式误认证为实数恒等式。
@pytest.mark.parametrize("declaration", ["real", "integer", "positive_real"])
@pytest.mark.parametrize(
    "left",
    [r"\sqrt{0-x^2}*\sqrt{0-x^2}", "(0-x^2)^(1/2)*(0-x^2)^(1/2)"],
)
def test_original_variable_roots_keep_real_domains_after_complex_simplification(left, declaration):
    result = verify_equivalence(
        parse_restricted_latex(left), parse_restricted_latex("0-x^2"),
        {"variables": {"x": declaration}},
    )
    if declaration == "positive_real":
        assert result.status == "uncertain"
        assert result.relation == "expression_not_defined"
    else:
        assert result.status == "contradicted"
        assert result.relation == "defined_domain_changed"
        assert result.evidence[0] == "{0}"


def test_solving_uses_original_root_domain_and_rejects_nowhere_real_expressions():
    ast = parse_restricted_latex(r"\sqrt{0-x^2}*\sqrt{0-x^2}=0-x^2")
    assert solve(ast, "x")["solution_set"] == "{0}"
    assert solve(ast, "x", {"variables": {"x": "positive_real"}}) == {
        "status": "uncertain", "reason_code": "expression_not_defined"
    }
    inverse_root = parse_restricted_latex("(0-x^2)^((0-1)/2)=1")
    assert solve(inverse_root, "x")["reason_code"] == "expression_not_defined"


def test_symbolic_real_powers_fail_closed_when_negative_bases_are_unresolved():
    ast = parse_restricted_latex("x^x")
    assert verify_equivalence(ast, ast).status == "uncertain"
    assert verify_equivalence(ast, ast, {"variables": {"x": "positive_real"}}).status == "verified"
    integer_power = parse_restricted_latex("(0-1)^x")
    assert verify_equivalence(integer_power, integer_power, {"variables": {"x": "integer"}}).status == "verified"


def test_integer_negative_powers_still_preserve_the_zero_exclusion():
    inverse = parse_restricted_latex("x^(0-1)")
    fraction = parse_restricted_latex("1/x")
    for declaration in ("real", "integer", "positive_real"):
        result = verify_equivalence(inverse, fraction, {"variables": {"x": declaration}})
        assert result.status == "verified"


@pytest.mark.parametrize("source", ["0^0", "0^(0-1)", "(x-x)^0"])
def test_zero_to_nonpositive_powers_is_undefined_in_school_algebra(source):
    ast = parse_restricted_latex(source)
    assert verify_equivalence(ast, ast).relation == "expression_not_defined"
    with pytest.raises(VerificationError, match="not defined over the reals"):
        normalize(ast)


def test_variable_zero_power_keeps_the_zero_exclusion():
    ast = parse_restricted_latex("x^0")
    one = parse_restricted_latex("1")
    assert verify_equivalence(ast, one).relation == "defined_domain_changed"
    assert verify_equivalence(ast, one, {"variables": {"x": "positive_real"}}).status == "verified"


def test_integer_symbolic_power_keeps_valid_negative_bases():
    ast = parse_restricted_latex("x^x")
    domain = {"variables": {"x": "integer"}}
    assert verify_equivalence(ast, ast, domain).status == "verified"
    equation = parse_restricted_latex("0*x^x=x+1")
    result = solve(equation, "x", domain)
    # 原式 x**x 在 -1 处仍有定义；即使乘以零后化简掉它，也不能丢失这个定义域。
    assert result["status"] == "verified"
    assert result["solution_set"] == "{-1}"


def test_zero_base_symbolic_power_requires_a_positive_exponent():
    ast = parse_restricted_latex("0^x")
    zero = parse_restricted_latex("0")
    assert verify_equivalence(ast, zero).relation == "defined_domain_changed"
    assert verify_equivalence(ast, zero, {"variables": {"x": "positive_real"}}).status == "verified"


def test_periodic_domains_compare_bound_indices_by_meaning():
    ast = parse_restricted_latex("tan(x)")
    assert verify_equivalence(ast, ast).status == "verified"
    result = verify_equivalence(ast, parse_restricted_latex("sin(x)/cos(x)"))
    assert result.relation != "defined_domain_changed"
