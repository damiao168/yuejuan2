from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import sympy
from sympy import S
from sympy.calculus.util import continuous_domain
from sympy.core.relational import Relational
from sympy.solvers.inequalities import solve_univariate_inequality


class VerificationError(ValueError):
    pass


@dataclass(frozen=True)
class VerificationResult:
    status: str
    relation: str
    domain: str
    constraints: tuple[str, ...]
    evidence: tuple[str, ...]
    engine: str = "sympy"
    engine_version: str = sympy.__version__
    ruleset_version: str = "yuejuan-math-rules-v1"


def normalize(ast: dict[str, Any], domain: dict[str, Any] | None = None) -> dict[str, Any]:
    assumptions = domain or {}
    _validate_real_constants(ast, assumptions)
    expression = ast_to_sympy(ast, assumptions)
    return {"srepr": sympy.srepr(expression), "canonical": str(expression)}


def verify_equivalence(left_ast: dict[str, Any], right_ast: dict[str, Any], domain: dict[str, Any] | None = None) -> VerificationResult:
    assumptions = domain or {}
    domain_name = str(assumptions.get("domain", "real"))
    constraints = tuple(str(item) for item in assumptions.get("constraints", []))
    if constraints or domain_name != "real":
        return VerificationResult("uncertain", "unsupported_domain_constraints", domain_name, constraints, ())
    try:
        _validate_real_constants(left_ast, assumptions)
        _validate_real_constants(right_ast, assumptions)
    except VerificationError:
        return VerificationResult("uncertain", "expression_not_defined", domain_name, constraints, ())
    left = ast_to_sympy(left_ast, assumptions); right = ast_to_sympy(right_ast, assumptions)
    variables = sorted(_ast_symbols(left_ast, assumptions) | _ast_symbols(right_ast, assumptions), key=str)
    left_domain = right_domain = S.Reals
    if len(variables) == 1:
        try:
            left_domain = _defined_domain(left_ast, variables[0], assumptions)
            right_domain = _defined_domain(right_ast, variables[0], assumptions)
        except (NotImplementedError, TypeError, ValueError):
            return VerificationResult("uncertain", "domain_not_resolved", domain_name, constraints, ())
    elif len(variables) > 1:
        return VerificationResult("uncertain", "multivariable_domain_not_resolved", domain_name, constraints, ())
    if left_domain is S.EmptySet or right_domain is S.EmptySet:
        return VerificationResult("uncertain", "expression_not_defined", domain_name, constraints, ())
    if left == right or (not isinstance(left, Relational) and not isinstance(right, Relational)):
        domains_equal = _domains_equal(left_domain, right_domain)
        if domains_equal is None:
            return VerificationResult("uncertain", "domain_not_resolved", domain_name, constraints, ())
        if not domains_equal:
            return VerificationResult("contradicted", "defined_domain_changed", domain_name, constraints, (str(left_domain), str(right_domain)))
    if left == right:
        return VerificationResult("verified", "exact_ast_equivalent", domain_name, constraints, ("exact_sympy_match",))
    # 方程比较各自合法域内的完整解集；仅证明式子相似不能排除增根或丢根。
    if isinstance(left, sympy.Equality) and isinstance(right, sympy.Equality) and len(variables) == 1:
        left_set = sympy.solveset(left, variables[0], domain=left_domain)
        right_set = sympy.solveset(right, variables[0], domain=right_domain)
        if left_set.has(sympy.ConditionSet) or right_set.has(sympy.ConditionSet):
            return VerificationResult("uncertain", "solution_set_not_resolved", domain_name, constraints, (str(left_set), str(right_set)))
        if left_set == right_set:
            return VerificationResult("verified", "solution_set_equivalent", domain_name, constraints, (str(left_set),))
        if left_set.is_subset(right_set) is True:
            return VerificationResult("contradicted", "extra_solutions_introduced", domain_name, constraints, (str(left_set), str(right_set)))
        if right_set.is_subset(left_set) is True:
            return VerificationResult("contradicted", "solutions_lost", domain_name, constraints, (str(left_set), str(right_set)))
        return VerificationResult("contradicted", "different_solution_set", domain_name, constraints, (str(left_set), str(right_set)))
    if not isinstance(left, Relational) and not isinstance(right, Relational):
        difference = sympy.cancel(sympy.together(left - right))
        if difference == 0:
            return VerificationResult("verified", "algebraically_equivalent", domain_name, constraints, ("together_cancel",))
        counterexample = _counterexample(left, right, left_domain)
        if counterexample:
            return VerificationResult("contradicted", "numeric_counterexample", domain_name, constraints, (counterexample,))
    return VerificationResult("uncertain", "unknown", domain_name, constraints, ())


def verify_transition(previous_ast: dict[str, Any], next_ast: dict[str, Any], domain: dict[str, Any] | None = None) -> VerificationResult:
    result = verify_equivalence(previous_ast, next_ast, domain)
    if result.status == "verified":
        return VerificationResult("verified", "equivalent_transform", result.domain, result.constraints, result.evidence)
    return result


def solve(ast: dict[str, Any], variable: str, domain: dict[str, Any] | None = None) -> dict[str, Any]:
    assumptions = domain or {}
    symbol = _symbols(assumptions).get(variable, sympy.Symbol(variable, real=True))
    if assumptions.get("constraints") or assumptions.get("domain", "real") != "real":
        return {"status": "uncertain", "reason_code": "unsupported_domain_constraints"}
    try:
        _validate_real_constants(ast, assumptions)
    except VerificationError:
        return {"status": "uncertain", "reason_code": "expression_not_defined"}
    expression = ast_to_sympy(ast, assumptions)
    if _ast_symbols(ast, assumptions) != {symbol}:
        return {"status": "uncertain", "reason_code": "unsupported_solve_variables"}
    try:
        universe = _defined_domain(ast, symbol, assumptions)
    except (NotImplementedError, TypeError, ValueError):
        return {"status": "uncertain", "reason_code": "domain_not_resolved"}
    if universe is S.EmptySet:
        return {"status": "uncertain", "reason_code": "expression_not_defined"}
    result = sympy.solveset(expression, symbol, domain=universe)
    if result.has(sympy.ConditionSet):
        return {"status": "uncertain", "reason_code": "solution_set_not_resolved", "solution_set": str(result)}
    return {"status": "verified", "solution_set": str(result), "engine": "sympy", "engine_version": sympy.__version__, "ruleset_version": "yuejuan-math-rules-v1"}


def ast_to_sympy(node: dict[str, Any], domain: dict[str, Any]) -> sympy.Basic:
    if not isinstance(node, dict): raise VerificationError("AST node must be an object")
    kind = node.get("kind"); value = str(node.get("value", "")); children = node.get("children", [])
    if not isinstance(children, list) or len(children) > 64: raise VerificationError("invalid AST children")
    parsed = [ast_to_sympy(child, domain) for child in children]
    # 书写的有限小数表示精确数学值，不能先转成二进制浮点数。
    if kind == "number": return sympy.Rational(value)
    if kind == "symbol":
        if not value.isalpha() or len(value) > 32: raise VerificationError("invalid symbol")
        return _symbols(domain).get(value, sympy.Symbol(value, real=True))
    if kind == "group" and len(parsed) == 1: return parsed[0]
    if kind == "operator" and len(parsed) == 2:
        if value == "+": return parsed[0] + parsed[1]
        if value == "-": return parsed[0] - parsed[1]
        if value == "*": return parsed[0] * parsed[1]
        raise VerificationError("unsupported operator")
    if kind == "fraction" and len(parsed) == 2: return parsed[0] / parsed[1]
    if kind == "power" and len(parsed) == 2: return parsed[0] ** parsed[1]
    if kind == "radical" and len(parsed) == 1: return sympy.sqrt(parsed[0])
    if kind == "function" and len(parsed) == 1 and value in {"sin", "cos", "tan", "abs"}:
        return {"sin": sympy.sin, "cos": sympy.cos, "tan": sympy.tan, "abs": sympy.Abs}[value](parsed[0])
    if kind == "equation" and len(parsed) == 2: return sympy.Eq(parsed[0], parsed[1], evaluate=False)
    if kind == "inequality" and len(parsed) == 2:
        operations = {"<": sympy.Lt, ">": sympy.Gt, "\\le": sympy.Le, "\\leq": sympy.Le, "\\ge": sympy.Ge, "\\geq": sympy.Ge, "\\neq": sympy.Ne}
        if value in operations: return operations[value](parsed[0], parsed[1])
    raise VerificationError(f"unsupported AST kind: {kind}")


def _ast_symbols(node: dict[str, Any], domain: dict[str, Any]) -> set[sympy.Symbol]:
    result = {ast_to_sympy(node, domain)} if node.get("kind") == "symbol" else set()
    for child in node.get("children", []):
        result.update(_ast_symbols(child, domain))
    return result


def _validate_real_constants(node: dict[str, Any], assumptions: dict[str, Any]) -> None:
    # 在化简隐藏未定义运算前检查每个原始子式，例如 sqrt(-1)*sqrt(-1) 不能被化简为实数 -1。
    if not isinstance(node, dict):
        raise VerificationError("AST node must be an object")
    children = node.get("children", [])
    if not isinstance(children, list) or len(children) > 64:
        raise VerificationError("invalid AST children")
    for child in children:
        _validate_real_constants(child, assumptions)
    if node.get("kind") == "power" and len(children) == 2:
        base = ast_to_sympy(children[0], assumptions)
        exponent = ast_to_sympy(children[1], assumptions)
        if base.is_zero is True and exponent.is_nonpositive is True:
            raise VerificationError("zero to a nonpositive power is not defined over the reals")
    if node.get("kind") not in {"equation", "inequality"}:
        expression = ast_to_sympy(node, assumptions)
        if not expression.free_symbols and (expression.is_real is not True or expression.is_finite is not True):
            raise VerificationError("constant expression is not defined over the reals")


def _variable_universe(variable: sympy.Symbol, assumptions: dict[str, Any]) -> sympy.Set:
    declared = str(assumptions.get("variables", {}).get(str(variable), "real")).lower()
    if declared == "integer":
        return S.Integers
    if declared == "positive_real":
        return sympy.Interval.open(0, sympy.oo)
    return S.Reals


def _defined_domain(node: dict[str, Any], variable: sympy.Symbol, assumptions: dict[str, Any]) -> sympy.Set:
    # 先按实数求定义域，再与声明全集求交；反复对整数范围做差和交集，可能留下形式不同但等价的 SymPy 集合。
    return _variable_universe(variable, assumptions).intersect(_real_defined_domain(node, variable, assumptions))


def _real_defined_domain(node: dict[str, Any], variable: sympy.Symbol, assumptions: dict[str, Any]) -> sympy.Set:
    # 遍历原始 AST，不能让 SymPy 化简抹掉 x/x 在零点的极点或根式的实数定义域限制。
    universe = _variable_universe(variable, assumptions)
    result = S.Reals
    children = node.get("children", [])
    for child in children:
        result = result.intersect(_real_defined_domain(child, variable, assumptions))
    if node.get("kind") == "fraction":
        denominator = ast_to_sympy(children[1], assumptions)
        excluded = sympy.solveset(denominator, variable, domain=S.Reals)
        if excluded.has(sympy.ConditionSet):
            raise VerificationError("denominator domain not resolved")
        result = result - excluded
    if node.get("kind") == "radical":
        # SymPy 可能把 sqrt(-x**2) 改写成 I*Abs(x)，连续域虽是全体实数，值却不是实数；
        # 因此要在改写前保留原根式的实数限制。
        radicand = ast_to_sympy(children[0], assumptions)
        result = result.intersect(_condition_domain(sympy.Ge(radicand, 0), variable))
    if node.get("kind") == "power":
        base = ast_to_sympy(children[0], assumptions)
        exponent = ast_to_sympy(children[1], assumptions)
        result = result.intersect(_real_power_domain(base, exponent, variable, universe))
    # 原始四则运算、根式和幂已在上面单独处理；对改写后的父式调用 continuous_domain 可能抹掉根式实数限制，
    # 也可能错误排除合法的负整数幂。支持的函数中只有 tan 需要额外限制。
    if node.get("kind") == "function" and node.get("value") == "tan":
        result = result.intersect(continuous_domain(ast_to_sympy(node, assumptions), variable, S.Reals))
    return result


def _condition_domain(condition: sympy.Basic, variable: sympy.Symbol) -> sympy.Set:
    if condition is S.true:
        return S.Reals
    if condition is S.false:
        return S.EmptySet
    result = solve_univariate_inequality(condition, variable, relational=False, domain=S.Reals)
    if not isinstance(result, sympy.Set) or result.has(sympy.ConditionSet):
        raise VerificationError("real expression domain not resolved")
    return result


def _domains_equal(left: sympy.Set, right: sympy.Set) -> bool | None:
    # 独立求出的周期定义域可能使用不同 Dummy 索引，不能只凭结构不等就断定定义域不同。
    if left == right or left.dummy_eq(right):
        return True
    try:
        left_subset = left.is_subset(right)
        right_subset = right.is_subset(left)
        if left_subset is True and right_subset is True:
            return True
        if left_subset is False or right_subset is False:
            return False
        empty = left.symmetric_difference(right).is_empty
    except (NotImplementedError, TypeError, ValueError):
        return None
    return empty if empty in (True, False) else None


def _real_power_domain(base: sympy.Basic, exponent: sympy.Basic, variable: sympy.Symbol, universe: sympy.Set) -> sympy.Set:
    if exponent.is_integer is True:
        if exponent.is_positive is True:
            return S.Reals
        nonzero_base = _condition_domain(sympy.Ne(base, 0), variable)
        if exponent.is_nonpositive is True:
            return nonzero_base
        # 整数幂允许负底数；指数为正时零底数才有效，基础代数不定义 0**0。
        return nonzero_base.union(_condition_domain(sympy.Gt(exponent, 0), variable))
    if exponent.is_number and exponent.is_real is True:
        # ast_to_sympy 使用主值幂：非整数实指数要求底数非负，负指数还要排除零。
        condition = sympy.Ge(base, 0) if exponent.is_positive is True else sympy.Gt(base, 0)
        return _condition_domain(condition, variable)
    if base.is_zero is True:
        return _condition_domain(sympy.Gt(exponent, 0), variable)
    positive_base = _condition_domain(sympy.Gt(base, 0), variable)
    if universe.is_subset(positive_base) is True:
        return universe
    # 负底数配符号实指数时，可行的整数指数可能是不连续集合；无法解析时必须转为不确定，不能静默丢掉。
    raise VerificationError("symbolic real power domain not resolved")


def _symbols(domain: dict[str, Any]) -> dict[str, sympy.Symbol]:
    out: dict[str, sympy.Symbol] = {}
    variables = domain.get("variables", {})
    if not isinstance(variables, dict): raise VerificationError("variables must be an object")
    for name, declared in variables.items():
        if not str(name).isalpha(): raise VerificationError("invalid variable name")
        kind = str(declared).lower()
        kwargs = {"real": True}
        if kind == "integer": kwargs = {"integer": True}
        elif kind == "positive_real": kwargs = {"real": True, "positive": True}
        elif kind not in {"real", "integer", "positive_real"}: raise VerificationError("unsupported variable domain")
        out[str(name)] = sympy.Symbol(str(name), **kwargs)
    return out


def _counterexample(left: sympy.Basic, right: sympy.Basic, domain: sympy.Set = S.Reals) -> str:
    variables = sorted(left.free_symbols | right.free_symbols, key=str)
    for value in (-3, -1, 0, 1, 2, 5):
        substitutions = {symbol: value + index for index, symbol in enumerate(variables)}
        try:
            if any((symbol.is_positive and number <= 0) or domain.contains(number) != S.true for symbol, number in substitutions.items()):
                continue
            difference = sympy.N(left.subs(substitutions) - right.subs(substitutions))
            if difference.has(S.NaN, S.ComplexInfinity, S.Infinity, S.NegativeInfinity):
                continue
            if difference != 0:
                return ",".join(f"{symbol}={substitutions[symbol]}" for symbol in variables)
        except (TypeError, ValueError, ZeroDivisionError):
            continue
    return ""
