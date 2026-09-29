#!/usr/bin/env python3
"""Deterministic generator for the synthetic-v2 MathBench fixture set.

Everything emitted by this script is SYNTHETIC data with no randomness and no
model involvement: ground truth is hand-authored, and the ``synthetic-v2``
predictions are derived from ground truth by injecting a fixed schedule of
deliberate mistakes so every metric path in ``run_math_bench.py`` gets both
hits and misses. Baselines computed from this data are a harness self-check
only. They are NOT model quality evidence and must never be quoted as pilot
results. The real MathBench (1000+ formula crops, 500+ full answers) waits for
de-identified real answer sheets.

``original`` manifest fields are virtual references: the runner scores JSON
documents only and never loads images, so no PNG files exist for this set.
"""

from __future__ import annotations

import argparse
import copy
import itertools
import json
import re
from pathlib import Path
from typing import Any

SUBJECT_CODE = "mathematics"
PREDICTION_SET = "synthetic-v2"
ORIGINAL_ENTRY_ID = "synthetic-equation-001"
ORIGINAL_PREDICTION_SOURCE = "predictions/smoke-v1/synthetic-equation-001.json"
ORIGINAL_PREFIX = "../../synthetic/math-recognition-synthetic-v2/pages"

# ---------------------------------------------------------------------------
# Restricted-AST builders (the same vocabulary math-verification-worker emits).
# ---------------------------------------------------------------------------


def sym(value: str) -> dict[str, Any]:
    return {"kind": "symbol", "value": value}


def num(value: str) -> dict[str, Any]:
    return {"kind": "number", "value": value}


def op(name: str, *children: Any) -> dict[str, Any]:
    return {"kind": "operator", "name": name, "children": list(children)}


def eq(lhs: Any, rhs: Any) -> dict[str, Any]:
    return {"kind": "equation", "children": [lhs, rhs]}


def ineq(operator: str, lhs: Any, rhs: Any) -> dict[str, Any]:
    return {"kind": "inequality", "operator": operator, "children": [lhs, rhs]}


def frac(numerator: Any, denominator: Any) -> dict[str, Any]:
    return {"kind": "fraction", "children": [numerator, denominator]}


def sqrt(radicand: Any) -> dict[str, Any]:
    return {"kind": "radical", "children": [radicand]}


def root(index: str, radicand: Any) -> dict[str, Any]:
    return {"kind": "radical", "index": index, "children": [radicand]}


def pow_(base: Any, exponent: Any) -> dict[str, Any]:
    return {"kind": "power", "children": [base, exponent]}


def subs_(base: Any, index: Any) -> dict[str, Any]:
    return {"kind": "subscript", "children": [base, index]}


def abs_(value: Any) -> dict[str, Any]:
    return {"kind": "absolute", "children": [value]}


def trig(name: str, argument: Any) -> dict[str, Any]:
    return {"kind": "trig", "name": name, "unit": "deg", "children": [argument]}


def vec(base: Any) -> dict[str, Any]:
    return {"kind": "vector", "children": [base]}


def cases(*equations: Any) -> dict[str, Any]:
    return {"kind": "system", "children": list(equations)}


def lim_(variable: Any, target: Any, body: Any, value: Any) -> dict[str, Any]:
    return {"kind": "limit", "children": [variable, target, body, value]}


def sum_(lower: Any, upper: Any, body: Any, value: Any) -> dict[str, Any]:
    return {"kind": "sum", "children": [lower, upper, body, value]}


def int_(lower: Any, upper: Any, body: Any, value: Any) -> dict[str, Any]:
    return {"kind": "integral", "children": [lower, upper, body, value]}


def mat(rows: list[list[Any]]) -> dict[str, Any]:
    return {"kind": "matrix", "rows": rows}


def sset(*members: Any) -> dict[str, Any]:
    return {"kind": "solution_set", "children": list(members)}


def angle_(vertex: Any) -> dict[str, Any]:
    return {"kind": "angle", "children": [vertex]}


def perp(lhs: Any, rhs: Any) -> dict[str, Any]:
    return {"kind": "perpendicular", "children": [lhs, rhs]}


def par(lhs: Any, rhs: Any) -> dict[str, Any]:
    return {"kind": "parallel", "children": [lhs, rhs]}


def cong(lhs: Any, rhs: Any) -> dict[str, Any]:
    return {"kind": "congruent", "children": [lhs, rhs]}


def implies(lhs: Any, rhs: Any) -> dict[str, Any]:
    return {"kind": "implication", "children": [lhs, rhs]}


def det_(matrix: Any) -> dict[str, Any]:
    return {"kind": "determinant", "children": [matrix]}


# ---------------------------------------------------------------------------
# Category matrix. Every entry follows the plan's required coverage list;
# each category carries >= 2 samples and the corpus totals 54 samples.
# ---------------------------------------------------------------------------

CATEGORIES: dict[str, list[dict[str, Any]]] = {
    "fraction-stacked": [
        {
            "formulas": [("formula-1", r"\frac{x+1}{2}=3", eq(frac(op("+", sym("x"), num("1")), num("2")), num("3"))), ("formula-2", "x=5", eq(sym("x"), num("5")))],
            "symbols": ["x", "+", "1", "2", "3", "5", "="],
            "steps": [("step-1", "(x+1)/2=3"), ("step-2", "x+1=6"), ("step-3", "x=5")],
        },
        {
            "formulas": [("formula-1", r"\frac{6}{x-1}=2", eq(frac(num("6"), op("-", sym("x"), num("1"))), num("2"))), ("formula-2", "x=4", None)],
            "symbols": ["6", "x", "-", "1", "2", "4", "="],
            "steps": [("step-1", "6/(x-1)=2"), ("step-2", "x-1=3"), ("step-3", "x=4")],
        },
        {
            "formulas": [("formula-1", r"\frac{2x}{3}=\frac{4}{9}", eq(frac(op("*", num("2"), sym("x")), num("3")), frac(num("4"), num("9")))), ("formula-2", r"x=\frac{2}{3}", eq(sym("x"), frac(num("2"), num("3"))))],
            "symbols": ["2", "x", "3", "4", "9", "="],
            "steps": [("step-1", "2x/3=4/9"), ("step-2", "18x=12"), ("step-3", "x=2/3")],
        },
    ],
    "fraction-inline": [
        {
            "formulas": [("formula-1", "(x+1)/(x-2)=3", eq(frac(op("+", sym("x"), num("1")), op("-", sym("x"), num("2"))), num("3"))), ("formula-2", "x=7/2", None)],
            "symbols": ["x", "+", "-", "1", "2", "3", "7", "="],
            "steps": [("step-1", "(x+1)/(x-2)=3"), ("step-2", "x+1=3x-6"), ("step-3", "2x=7"), ("step-4", "x=7/2")],
        },
        {
            "formulas": [("formula-1", r"3/4\times 8/9=2/3", eq(op("*", frac(num("3"), num("4")), frac(num("8"), num("9"))), frac(num("2"), num("3"))))],
            "symbols": ["3", "4", "8", "9", "2", "×", "="],
            "steps": [("step-1", "3/4×8/9=24/36"), ("step-2", "24/36=2/3")],
            "rubric": [("fraction_multiplication", "supported")],
        },
    ],
    "radical": [
        {
            "formulas": [("formula-1", r"\sqrt{x+1}=3", eq(sqrt(op("+", sym("x"), num("1"))), num("3"))), ("formula-2", "x=8", None)],
            "symbols": ["√", "x", "+", "1", "3", "8", "="],
            "steps": [("step-1", "√(x+1)=3"), ("step-2", "x+1=9"), ("step-3", "x=8")],
        },
        {
            "formulas": [("formula-1", r"\sqrt{2}\times\sqrt{8}=4", eq(op("*", sqrt(num("2")), sqrt(num("8"))), num("4"))), ("formula-2", r"\sqrt[3]{27}=3", eq(root("3", num("27")), num("3")))],
            "symbols": ["√", "2", "8", "4", "3", "27", "×", "="],
            "steps": [("step-1", "√2×√8=√16"), ("step-2", "√16=4")],
        },
    ],
    "exponent": [
        {
            "formulas": [("formula-1", "x^2=4", eq(pow_(sym("x"), num("2")), num("4"))), ("formula-2", r"x=\pm 2", sset(eq(sym("x"), num("2")), eq(sym("x"), num("-2"))))],
            "symbols": ["x", "2", "4", "±", "="],
            "steps": [("step-1", "x^2=4"), ("step-2", "x=2或x=-2")],
            "rubric": [("solve_quadratic", "supported")],
        },
        {
            "formulas": [("formula-1", "2^{n+1}=8", eq(pow_(num("2"), op("+", sym("n"), num("1"))), num("8"))), ("formula-2", "n=2", None)],
            "symbols": ["2", "n", "1", "8", "="],
            "steps": [("step-1", "2^(n+1)=8"), ("step-2", "n+1=3"), ("step-3", "n=2")],
        },
    ],
    "subscript": [
        {
            "formulas": [("formula-1", "x_1=3", eq(subs_(sym("x"), num("1")), num("3"))), ("formula-2", "x_2=-5", eq(subs_(sym("x"), num("2")), num("-5")))],
            "symbols": ["x", "1", "2", "3", "5", "-", "=", "_"],
            "steps": [("step-1", "x_1+x_2=-2"), ("step-2", "x_1=3"), ("step-3", "x_2=-5")],
        },
        {
            "formulas": [("formula-1", "a_{n+1}=2a_n", eq(subs_(sym("a"), op("+", sym("n"), num("1"))), op("*", num("2"), subs_(sym("a"), sym("n"))))), ("formula-2", "a_2=6", None)],
            "symbols": ["a", "n", "1", "2", "6", "="],
            "steps": [("step-1", "a_(n+1)=2a_n"), ("step-2", "a_1=3"), ("step-3", "a_2=6")],
        },
    ],
    "system-of-equations": [
        {
            "formulas": [("formula-1", r"\begin{cases}x+y=3\\x-y=1\end{cases}", cases(eq(op("+", sym("x"), sym("y")), num("3")), eq(op("-", sym("x"), sym("y")), num("1")))), ("formula-2", "x=2,y=1", sset(eq(sym("x"), num("2")), eq(sym("y"), num("1"))))],
            "symbols": ["x", "y", "3", "1", "2", "=", "{", "}"],
            "relations": [("formula-1", "formula-2", "solves")],
            "steps": [("step-1", "x+y=3"), ("step-2", "x-y=1"), ("step-3", "2x=4"), ("step-4", "x=2,y=1")],
            "rubric": [("solve_system_of_equations", "supported")],
        },
        {
            "formulas": [("formula-1", r"\begin{cases}2x+3y=12\\x=3\end{cases}", cases(eq(op("+", op("*", num("2"), sym("x")), op("*", num("3"), sym("y"))), num("12")), eq(sym("x"), num("3")))), ("formula-2", "y=2", None)],
            "symbols": ["x", "y", "2", "3", "12", "="],
            "relations": [("formula-1", "formula-2", "solves")],
            "steps": [("step-1", "2x+3y=12"), ("step-2", "x=3"), ("step-3", "6+3y=12"), ("step-4", "y=2")],
            "rubric": [("solve_system_of_equations", "supported")],
        },
    ],
    "inequality": [
        {
            "formulas": [("formula-1", r"2x-1\le 5", ineq("<=", op("-", op("*", num("2"), sym("x")), num("1")), num("5"))), ("formula-2", r"x\le 3", ineq("<=", sym("x"), num("3")))],
            "symbols": ["x", "2", "1", "5", "3", "≤", "="],
            "steps": [("step-1", "2x-1≤5"), ("step-2", "2x≤6"), ("step-3", "x≤3")],
            "rubric": [("solve_inequality", "supported")],
        },
        {
            "formulas": [("formula-1", r"-2x\ge 4", ineq(">=", op("-", op("*", num("2"), sym("x"))), num("4"))), ("formula-2", r"x\le -2", ineq("<=", sym("x"), num("-2")))],
            "symbols": ["x", "2", "4", "-", "≥", "≤"],
            "steps": [("step-1", "-2x≥4"), ("step-2", "x≤-2(方向翻转)")],
            "rubric": [("flip_inequality_direction", "supported")],
        },
    ],
    "absolute-value": [
        {
            "formulas": [("formula-1", "|x-1|=2", eq(abs_(op("-", sym("x"), num("1"))), num("2"))), ("formula-2", r"x=3 \text{或} x=-1", sset(eq(sym("x"), num("3")), eq(sym("x"), num("-1"))))],
            "symbols": ["|", "x", "-", "1", "2", "3", "="],
            "steps": [("step-1", "|x-1|=2"), ("step-2", "x-1=2或x-1=-2"), ("step-3", "x=3或x=-1")],
            "rubric": [("solve_absolute_value_equation", "supported")],
        },
        {
            "formulas": [("formula-1", r"|2x+3|\le 5", ineq("<=", abs_(op("+", op("*", num("2"), sym("x")), num("3"))), num("5"))), ("formula-2", r"-4\le x\le 1", None)],
            "symbols": ["|", "x", "2", "3", "5", "4", "1", "≤"],
            "steps": [("step-1", "|2x+3|≤5"), ("step-2", "-5≤2x+3≤5"), ("step-3", "-4≤x≤1")],
            "rubric": [("solve_absolute_value_inequality", "supported")],
        },
    ],
    "trigonometric": [
        {
            "formulas": [("formula-1", r"\sin 30^\circ=\frac{1}{2}", eq(trig("sin", num("30")), frac(num("1"), num("2")))), ("formula-2", r"\cos 60^\circ=\frac{1}{2}", eq(trig("cos", num("60")), frac(num("1"), num("2"))))],
            "symbols": ["sin", "cos", "30", "60", "°", "1", "2", "="],
            "steps": [("step-1", "sin30°=1/2"), ("step-2", "cos60°=1/2")],
            "rubric": [("recall_special_trig_values", "supported")],
        },
        {
            "formulas": [("formula-1", r"\cos 45^\circ=\frac{\sqrt{2}}{2}", eq(trig("cos", num("45")), frac(sqrt(num("2")), num("2")))), ("formula-2", r"\tan 60^\circ=\sqrt{3}", eq(trig("tan", num("60")), sqrt(num("3"))))],
            "symbols": ["cos", "tan", "45", "60", "°", "√", "2", "3", "="],
            "steps": [("step-1", "cos45°=√2/2"), ("step-2", "tan60°=√3")],
            "rubric": [("recall_special_trig_values", "supported")],
        },
    ],
    "geometry-symbol": [
        {
            "formulas": [("formula-1", r"\angle A=60^\circ", eq(angle_(sym("A")), num("60"))), ("formula-2", r"AB\perp CD", perp(sym("AB"), sym("CD"))), ("formula-3", r"AB\parallel EF", par(sym("AB"), sym("EF")))],
            "symbols": ["∠", "A", "60", "°", "⊥", "∥", "AB", "CD", "EF"],
            "relations": [("formula-1", "formula-2", "left_of"), ("formula-2", "formula-3", "left_of")],
            "steps": [("step-1", "∠A=60°"), ("step-2", "AB⊥CD"), ("step-3", "AB∥EF")],
            "rubric": [("identify_perpendicular_and_parallel", "supported")],
        },
        {
            "formulas": [("formula-1", r"\triangle ABC\cong\triangle DEF", cong(sym("ABC"), sym("DEF"))), ("formula-2", r"AC\parallel DF", par(sym("AC"), sym("DF")))],
            "symbols": ["△", "≅", "∥", "ABC", "DEF", "AC", "DF"],
            "relations": [("formula-1", "formula-2", "annotates")],
            "steps": [("step-1", "△ABC≅△DEF"), ("step-2", "对应边相等")],
            "rubric": [("prove_congruent_triangles", "supported")],
        },
    ],
    "vector": [
        {
            "formulas": [("formula-1", r"\overrightarrow{AB}+\overrightarrow{BC}=\overrightarrow{AC}", eq(op("+", vec(sym("AB")), vec(sym("BC"))), vec(sym("AC"))))],
            "symbols": ["→", "AB", "BC", "AC", "+", "="],
            "steps": [("step-1", "AB向量+BC向量=AC向量"), ("step-2", "首尾相接法则")],
            "rubric": [("apply_vector_triangle_rule", "supported")],
        },
        {
            "formulas": [("formula-1", r"|\vec{a}|=3", eq(abs_(vec(sym("a"))), num("3"))), ("formula-2", r"\vec{a}\cdot\vec{b}=|\vec{a}||\vec{b}|\cos\theta", None)],
            "symbols": ["|", "a", "b", "3", "·", "cos", "θ", "="],
            "relations": [("formula-1", "formula-2", "defines")],
            "steps": [("step-1", "|a|=3"), ("step-2", "a·b=|a||b|cosθ")],
            "rubric": [("apply_dot_product_definition", "supported")],
        },
    ],
    "limit": [
        {
            "formulas": [("formula-1", r"\lim_{x\to 0}\frac{\sin x}{x}=1", lim_(sym("x"), num("0"), frac(trig("sin", sym("x")), sym("x")), num("1")))],
            "symbols": ["lim", "x", "0", "sin", "1", "="],
            "steps": [("step-1", "lim(x→0) sinx/x"), ("step-2", "=1")],
            "rubric": [("evaluate_limit", "supported")],
        },
        {
            "formulas": [("formula-1", r"\lim_{n\to\infty}\frac{1}{n}=0", lim_(sym("n"), sym("∞"), frac(num("1"), sym("n")), num("0")))],
            "symbols": ["lim", "n", "∞", "1", "0", "="],
            "steps": [("step-1", "lim(n→∞) 1/n"), ("step-2", "=0")],
            "rubric": [("evaluate_limit", "supported")],
        },
    ],
    "summation": [
        {
            "formulas": [("formula-1", r"\sum_{i=1}^{10}i=55", sum_(eq(sym("i"), num("1")), num("10"), sym("i"), num("55")))],
            "symbols": ["Σ", "i", "1", "10", "55", "="],
            "steps": [("step-1", "Σ(i=1..10) i"), ("step-2", "=55")],
            "rubric": [("evaluate_finite_sum", "supported")],
        },
        {
            "formulas": [("formula-1", r"\sum_{k=1}^{n}k^2=\frac{n(n+1)(2n+1)}{6}", sum_(eq(sym("k"), num("1")), sym("n"), pow_(sym("k"), num("2")), frac(op("*", op("*", sym("n"), op("+", sym("n"), num("1"))), op("+", op("*", num("2"), sym("n")), num("1"))), num("6"))))],
            "symbols": ["Σ", "k", "n", "1", "2", "6", "="],
            "steps": [("step-1", "Σ(k=1..n) k²"), ("step-2", "=n(n+1)(2n+1)/6")],
            "rubric": [("apply_sum_formula", "supported")],
        },
    ],
    "integral": [
        {
            "formulas": [("formula-1", r"\int_0^1 x\,dx=\frac{1}{2}", int_(num("0"), num("1"), sym("x"), frac(num("1"), num("2"))))],
            "symbols": ["∫", "0", "1", "x", "dx", "="],
            "steps": [("step-1", "∫₀¹ x dx"), ("step-2", "=1/2")],
            "rubric": [("evaluate_definite_integral", "supported")],
        },
        {
            "formulas": [("formula-1", r"\int_1^e \frac{1}{x}dx=1", int_(num("1"), sym("e"), frac(num("1"), sym("x")), num("1")))],
            "symbols": ["∫", "1", "e", "x", "dx", "="],
            "steps": [("step-1", "∫₁^e (1/x) dx"), ("step-2", "=1")],
            "rubric": [("evaluate_definite_integral", "supported")],
        },
    ],
    "matrix": [
        {
            "formulas": [("formula-1", r"\begin{pmatrix}1&2\\3&4\end{pmatrix}", mat([[num("1"), num("2")], [num("3"), num("4")]])), ("formula-2", r"\det A=-2", eq(det_(sym("A")), num("-2")))],
            "symbols": ["1", "2", "3", "4", "det", "A", "-", "="],
            "relations": [("formula-2", "formula-1", "annotates")],
            "steps": [("step-1", "A=[[1,2],[3,4]]"), ("step-2", "detA=1×4-2×3=-2")],
            "rubric": [("compute_determinant", "supported")],
        },
        {
            "formulas": [("formula-1", r"\begin{pmatrix}x\\y\end{pmatrix}=\begin{pmatrix}2\\1\end{pmatrix}", eq(mat([[sym("x")], [sym("y")]]), mat([[num("2")], [num("1")]])))],
            "symbols": ["x", "y", "2", "1", "="],
            "steps": [("step-1", "(x,y)ᵀ=(2,1)ᵀ"), ("step-2", "x=2,y=1")],
            "rubric": [("read_matrix_solution", "supported")],
        },
    ],
    "chinese-mixed": [
        {
            "formulas": [("formula-1", "2x-1=7", eq(op("-", op("*", num("2"), sym("x")), num("1")), num("7"))), ("formula-2", "x=4", None)],
            "symbols": ["x", "2", "1", "7", "4", "="],
            "steps": [("step-1", "因为2x-1=7"), ("step-2", "所以2x=8"), ("step-3", "所以x=4")],
            "rubric": [("solve_linear_equation", "supported"), ("write_reasoning", "supported")],
        },
        {
            "formulas": [("formula-1", "3x+2x=60", eq(op("+", op("*", num("3"), sym("x")), op("*", num("2"), sym("x"))), num("60"))), ("formula-2", "x=12", None)],
            "symbols": ["x", "3", "2", "60", "12", "="],
            "steps": [("step-1", "设乙的速度为x千米/时"), ("step-2", "3x+2x=60"), ("step-3", "所以x=12"), ("step-4", "答:乙的速度为12千米/时")],
            "rubric": [("model_word_problem", "supported"), ("write_reasoning", "supported")],
        },
    ],
    "horizontal-step": [
        {
            "formulas": [("formula-1", r"x^2=4\Rightarrow x=\pm 2", implies(eq(pow_(sym("x"), num("2")), num("4")), sset(eq(sym("x"), num("2")), eq(sym("x"), num("-2")))))],
            "symbols": ["x", "2", "4", "±", "→", "="],
            "edge_kind": "arrow_right",
            "steps": [("step-1", "x^2=4 → x=±2"), ("step-2", "检验:(±2)²=4")],
            "rubric": [("solve_quadratic", "supported")],
        },
        {
            "formulas": [("formula-1", r"3(x-1)=6\Rightarrow x=3", implies(eq(op("*", num("3"), op("-", sym("x"), num("1"))), num("6")), eq(sym("x"), num("3"))))],
            "symbols": ["x", "3", "1", "6", "→", "="],
            "edge_kind": "arrow_right",
            "steps": [("step-1", "3(x-1)=6 → x-1=2"), ("step-2", "→ x=3")],
            "rubric": [("solve_linear_equation", "supported")],
        },
    ],
    "vertical-step": [
        {
            "formulas": [("formula-1", "2x+1=7", eq(op("+", op("*", num("2"), sym("x")), num("1")), num("7"))), ("formula-2", "x=3", None)],
            "symbols": ["x", "2", "1", "7", "3", "="],
            "edge_kind": "arrow_down",
            "steps": [("step-1", "2x+1=7"), ("step-2", "2x=6"), ("step-3", "x=3")],
        },
        {
            "formulas": [("formula-1", "x^2-5x+6=0", eq(op("-", op("+", pow_(sym("x"), num("2")), num("6")), op("*", num("5"), sym("x"))), num("0"))), ("formula-2", "x_1=2,x_2=3", None)],
            "symbols": ["x", "1", "2", "3", "5", "6", "0", "="],
            "edge_kind": "arrow_down",
            "steps": [("step-1", "x²-5x+6=0"), ("step-2", "(x-2)(x-3)=0"), ("step-3", "x₁=2"), ("step-4", "x₂=3")],
            "rubric": [("solve_quadratic", "supported")],
        },
    ],
    "two-column": [
        {
            "formulas": [("formula-1", "y=2x", eq(sym("y"), op("*", num("2"), sym("x")))), ("formula-2", "x+y=3", eq(op("+", sym("x"), sym("y")), num("3")))],
            "symbols": ["x", "y", "2", "3", "="],
            "relations": [("formula-1", "formula-2", "parallel_column")],
            "edges": [("col-a-1", "col-a-2", "next"), ("col-a-2", "col-a-3", "next"), ("col-b-1", "col-b-2", "next"), ("col-b-2", "col-b-3", "next"), ("col-a-3", "col-b-3", "cross_check")],
            "steps": [("col-a-1", "左栏代入法:y=2x"), ("col-a-2", "x+2x=3"), ("col-a-3", "x=1"), ("col-b-1", "右栏加减法:y-2x=0"), ("col-b-2", "3x=3"), ("col-b-3", "x=1")],
            "rubric": [("solve_system_substitution", "supported"), ("solve_system_elimination", "supported")],
        },
        {
            "formulas": [("formula-1", r"S=5\times 4", eq(sym("S"), op("*", num("5"), num("4")))), ("formula-2", "C=2(5+4)", eq(sym("C"), op("*", num("2"), op("+", num("5"), num("4")))))],
            "symbols": ["S", "C", "5", "4", "2", "×", "="],
            "relations": [("formula-1", "formula-2", "side_by_side")],
            "edges": [("col-a-1", "col-b-1", "parallel_column")],
            "steps": [("col-a-1", "左栏:S=5×4=20"), ("col-b-1", "右栏:C=2(5+4)=18")],
            "rubric": [("compute_area", "supported"), ("compute_perimeter", "supported")],
        },
    ],
    "insert": [
        {
            "formulas": [("formula-1", "2x=6", eq(op("*", num("2"), sym("x")), num("6"))), ("formula-2", "x=3", eq(sym("x"), num("3")))],
            "symbols": ["x", "2", "6", "3", "="],
            "relations": [("formula-2", "formula-1", "inserted_after")],
            "steps": [("step-1", "2x=6"), ("step-2", "x=3(补写)")],
            "rubric": [("final_answer_written", "supported")],
        },
        {
            "formulas": [("formula-1", "3x=12", eq(op("*", num("3"), sym("x")), num("12"))), ("formula-2", "x=4", eq(sym("x"), num("4")))],
            "symbols": ["x", "3", "12", "4", "="],
            "relations": [("formula-2", "formula-1", "inserted_after")],
            "steps": [("step-1", "3x=12"), ("step-2", "x=4(插入行间)")],
            "rubric": [("final_answer_written", "supported")],
        },
    ],
    "crossed-out": [
        {
            "formulas": [("formula-1", "x=5", eq(sym("x"), num("5"))), ("formula-2", "x=-5", eq(sym("x"), num("-5")))],
            "symbols": ["x", "5", "-", "="],
            "relations": [("formula-2", "formula-1", "replaces")],
            "steps": [("step-1", "x²=25"), ("step-2", "x=5(划除)"), ("step-3", "x=-5")],
            "rubric": [("final_answer_written", "supported")],
            "safe": False,
            "risky": True,
        },
        {
            "formulas": [("formula-1", r"\sin 45^\circ=\frac{\sqrt{3}}{2}", eq(trig("sin", num("45")), frac(sqrt(num("3")), num("2")))), ("formula-2", r"\sin 45^\circ=\frac{\sqrt{2}}{2}", eq(trig("sin", num("45")), frac(sqrt(num("2")), num("2"))))],
            "symbols": ["sin", "45", "°", "√", "2", "3", "="],
            "relations": [("formula-2", "formula-1", "replaces")],
            "steps": [("step-1", "sin45°=√3/2(划除)"), ("step-2", "sin45°=√2/2")],
            "rubric": [("recall_special_trig_values", "supported")],
        },
    ],
    "scratch": [
        {
            "formulas": [("formula-1", r"15\times 4=60", eq(op("*", num("15"), num("4")), num("60"))), ("formula-2", r"60\div 2=30", eq(frac(num("60"), num("2")), num("30")))],
            "symbols": ["15", "4", "60", "2", "30", "×", "÷", "="],
            "edges": [("scratch-1", "scratch-2", "scratch_next"), ("scratch-2", "step-1", "promoted_to")],
            "steps": [("scratch-1", "草稿:15×4=60"), ("scratch-2", "草稿:60÷2=30"), ("step-1", "总价=30元")],
            "rubric": [("compute_total_price", "supported")],
        },
        {
            "formulas": [("formula-1", "2^3=8", eq(pow_(num("2"), num("3")), num("8"))), ("formula-2", "V=8", eq(sym("V"), num("8")))],
            "symbols": ["2", "3", "8", "V", "="],
            "edges": [("scratch-1", "step-1", "scratch_of")],
            "steps": [("scratch-1", "草稿:2³=8"), ("step-1", "V=S×h=8")],
            "rubric": [("compute_volume", "supported")],
        },
    ],
    "low-quality": [
        {
            "formulas": [("formula-1", "x=2", eq(sym("x"), num("2")))],
            "symbols": ["x", "=", "2"],
            "steps": [("step-1", "x=2(字迹模糊)")],
            "verifications": [("verification-1", "unverified")],
            "rubric": [("final_answer_written", "supported")],
            "safe": False,
            "risky": True,
        },
        {
            "formulas": [("formula-1", r"\frac{x}{2}=3", eq(frac(sym("x"), num("2")), num("3"))), ("formula-2", "x=6", None)],
            "symbols": ["x", "2", "3", "6", "="],
            "steps": [("step-1", "x/2=3(部分褪色)"), ("step-2", "x=6")],
            "verifications": [("verification-1", "unverified")],
            "rubric": [("solve_linear_equation", "supported")],
            "safe": False,
            "risky": True,
        },
        {
            "formulas": [("formula-1", r"\angle B=45^\circ", eq(angle_(sym("B")), num("45")))],
            "symbols": ["∠", "B", "45", "°", "="],
            "steps": [("step-1", "∠B=45°(扫描模糊)")],
            "verifications": [("verification-1", "unverified")],
            "rubric": [("read_angle_from_figure", "supported")],
            "safe": False,
            "risky": True,
        },
    ],
    "alternative-method": [
        {
            "formulas": [("formula-1", "x^2-5x+6=0", eq(op("-", op("+", pow_(sym("x"), num("2")), num("6")), op("*", num("5"), sym("x"))), num("0"))), ("formula-2", "x=2,x=3", sset(eq(sym("x"), num("2")), eq(sym("x"), num("3"))))],
            "symbols": ["x", "2", "3", "5", "6", "0", "="],
            "edges": [("method-a-1", "method-a-2", "next"), ("method-a-2", "method-a-3", "next"), ("method-b-1", "method-b-2", "next"), ("method-b-2", "method-b-3", "next"), ("method-a-3", "method-b-3", "alternative_to")],
            "steps": [("method-a-1", "因式分解:x²-5x+6=0"), ("method-a-2", "(x-2)(x-3)=0"), ("method-a-3", "x=2或x=3"), ("method-b-1", "公式法:x²-5x+6=0"), ("method-b-2", "x=(5±√(25-24))/2"), ("method-b-3", "x=2或x=3")],
            "rubric": [("solve_quadratic", "supported"), ("alternative_method_accepted", "supported")],
        },
        {
            "formulas": [("formula-1", "y=x+1", eq(sym("y"), op("+", sym("x"), num("1")))), ("formula-2", "x+y=5", eq(op("+", sym("x"), sym("y")), num("5")))],
            "symbols": ["x", "y", "1", "5", "="],
            "edges": [("method-a-1", "method-a-2", "next"), ("method-a-2", "method-a-3", "next"), ("method-b-1", "method-b-2", "next"), ("method-b-2", "method-b-3", "next"), ("method-a-3", "method-b-3", "alternative_to")],
            "steps": [("method-a-1", "代入消元:y=x+1"), ("method-a-2", "x+x+1=5"), ("method-a-3", "x=2,y=3"), ("method-b-1", "加减消元:y-x=1"), ("method-b-2", "y+x=5"), ("method-b-3", "2y=6,y=3")],
            "rubric": [("solve_system_substitution", "supported"), ("solve_system_elimination", "supported")],
        },
        {
            "formulas": [("formula-1", "AB=5", eq(sym("AB"), num("5")))],
            "symbols": ["AB", "5", "="],
            "edges": [("method-a-1", "method-a-2", "next"), ("method-a-2", "method-a-3", "next"), ("method-b-1", "method-b-2", "next"), ("method-a-3", "method-b-2", "alternative_to")],
            "steps": [("method-a-1", "几何法:作高AD"), ("method-a-2", "BD=3,AD=4"), ("method-a-3", "AB=5"), ("method-b-1", "代数法:AB²=3²+4²"), ("method-b-2", "AB=5")],
            "rubric": [("pythagorean_theorem", "supported"), ("alternative_method_accepted", "supported")],
        },
    ],
    "solution-chain": [
        {
            "formulas": [("formula-1", "2x+1=7", eq(op("+", op("*", num("2"), sym("x")), num("1")), num("7"))), ("formula-2", "2x=6", eq(op("*", num("2"), sym("x")), num("6"))), ("formula-3", "x=3", eq(sym("x"), num("3")))],
            "symbols": ["x", "2", "1", "7", "6", "3", "="],
            "edge_kind": "derives",
            "steps": [("step-1", "2x+1=7"), ("step-2", "2x=6"), ("step-3", "x=3")],
            "rubric": [("solve_linear_equation", "supported"), ("show_steps", "supported")],
        },
        {
            "formulas": [("formula-1", r"\frac{x}{2}+\frac{x}{3}=5", eq(op("+", frac(sym("x"), num("2")), frac(sym("x"), num("3"))), num("5"))), ("formula-2", "5x=30", eq(op("*", num("5"), sym("x")), num("30"))), ("formula-3", "x=6", None)],
            "symbols": ["x", "2", "3", "5", "30", "6", "="],
            "edge_kind": "derives",
            "steps": [("step-1", "x/2+x/3=5"), ("step-2", "3x+2x=30"), ("step-3", "5x=30"), ("step-4", "x=6")],
            "rubric": [("solve_linear_equation", "supported"), ("show_steps", "supported")],
        },
        {
            "formulas": [("formula-1", r"2x-3\ge 5", ineq(">=", op("-", op("*", num("2"), sym("x")), num("3")), num("5"))), ("formula-2", r"x\ge 4", ineq(">=", sym("x"), num("4")))],
            "symbols": ["x", "2", "3", "5", "4", "≥", "="],
            "edge_kind": "derives",
            "steps": [("step-1", "2x-3≥5"), ("step-2", "2x≥8"), ("step-3", "x≥4"), ("step-4", "检验:2×4-3=5成立")],
            "rubric": [("solve_inequality", "supported"), ("show_steps", "supported")],
        },
    ],
}

# Deterministic defect schedule: position = global sample index % len(cycle).
# Each defect appears at least three times across the 54-sample corpus so every
# metric observes both hits and misses.
DEFECT_CYCLE = (
    "exact",
    "latex_normalization_only",
    "latex_variant_ast_equal",
    "relation_error",
    "symbol_error",
    "exact",
    "step_grouping_error",
    "equivalence_error",
    "unsafe_suggestion",
    "latex_normalization_only",
    "ast_mismatch",
    "edge_kind_error",
    "risky_recalled",
    "rubric_error",
    "latex_variant_ast_equal",
    "risky_missed",
)


# ---------------------------------------------------------------------------
# Spec normalization.
# ---------------------------------------------------------------------------


def normalized_spec(spec: dict[str, Any]) -> dict[str, Any]:
    steps = [{"id": sid, "normalized_text": text} for sid, text in spec["steps"]]
    if "edges" in spec:
        edges = [
            {"from_step_id": f, "to_step_id": t, "kind": k} for f, t, k in spec["edges"]
        ]
    else:
        kind = spec.get("edge_kind", "next")
        ids = [step["id"] for step in steps]
        edges = [
            {"from_step_id": a, "to_step_id": b, "kind": kind}
            for a, b in itertools.pairwise(ids)
        ]
    return {
        "formulas": [
            {"id": fid, "latex": latex, "ast": ast} for fid, latex, ast in spec["formulas"]
        ],
        "symbols": list(spec["symbols"]),
        "relations": [
            {"from_id": f, "to_id": t, "kind": k} for f, t, k in spec.get("relations", [])
        ],
        "solution_graph": {"steps": steps, "edges": edges},
        "verifications": [
            {"id": vid, "kind": "equivalence", "status": status}
            for vid, status in spec.get("verifications", [("verification-1", "verified")])
        ],
        "rubric_evidence": [
            {"rubric_criterion_key": key, "status": status}
            for key, status in spec.get("rubric", [("solve_equation", "supported")])
        ],
        "safe_to_suggest": spec.get("safe", True),
        "risky_case": spec.get("risky", False),
    }


# ---------------------------------------------------------------------------
# Deliberate prediction mutations (all deterministic).
# ---------------------------------------------------------------------------


def spacing_variant(latex: str) -> str:
    variant = latex.replace("=", " = ")
    if "(" in latex:
        variant = variant.replace("(", "\\left(").replace(")", "\\right)")
    return variant


def semantic_variant(latex: str) -> str:
    if r"\frac" in latex:
        return latex.replace(r"\frac", r"\tfrac", 1)
    if re.search(r"(?<=\d)(?=[A-Za-z])", latex):
        return re.sub(r"(?<=\d)(?=[A-Za-z])", r"\\cdot ", latex, count=1)
    if r"\sqrt{" in latex:
        return latex.replace(r"\sqrt{", r"\sqrt[2]{", 1)
    return latex + "{}"


def swapped_ast(ast: dict[str, Any]) -> dict[str, Any]:
    children = ast.get("children")
    if isinstance(children, list) and len(children) >= 2:
        mutated = copy.deepcopy(ast)
        mutated["children"] = list(reversed(children))
        return mutated
    return {"kind": "paren", "children": [copy.deepcopy(ast)]}


def flipped_status(status: str) -> str:
    return {"verified": "unverified", "unverified": "verified"}.get(status, "verified")


def mutated_prediction(truth: dict[str, Any], defect: str) -> dict[str, Any]:
    pred = copy.deepcopy(truth)
    formulas = pred["formulas"]
    if defect == "latex_normalization_only" and formulas:
        formulas[0]["latex"] = spacing_variant(formulas[0]["latex"])
    elif defect == "latex_variant_ast_equal" and formulas:
        formulas[0]["latex"] = semantic_variant(formulas[0]["latex"])
    elif defect == "ast_mismatch" and formulas and formulas[0]["ast"] is not None:
        formulas[0]["ast"] = swapped_ast(formulas[0]["ast"])
    elif defect == "symbol_error":
        if pred["symbols"]:
            pred["symbols"] = pred["symbols"][:-1]
        pred["symbols"] = pred["symbols"] + (["Δ"] if "Δ" not in pred["symbols"] else ["ω"])
    elif defect == "relation_error":
        relations = pred["relations"]
        if len(relations) >= 2:
            relations = relations[:-1]
        target = formulas[0]["id"] if formulas else "formula-1"
        relations = relations + [{"from_id": "region-x", "to_id": target, "kind": "spurious_overlap"}]
        pred["relations"] = relations
    elif defect == "step_grouping_error":
        steps = pred["solution_graph"]["steps"]
        if len(steps) >= 2:
            merged = {
                "id": steps[0]["id"],
                "normalized_text": steps[0]["normalized_text"] + ";" + steps[1]["normalized_text"],
            }
            pred["solution_graph"]["steps"] = [merged] + steps[2:]
    elif defect == "edge_kind_error":
        edges = pred["solution_graph"]["edges"]
        if edges:
            edges[-1]["kind"] = "spurious_jump"
    elif defect == "equivalence_error":
        for verification in pred["verifications"]:
            if verification["kind"] == "equivalence":
                verification["status"] = flipped_status(verification["status"])
                break
    elif defect == "rubric_error":
        evidence = pred["rubric_evidence"]
        if evidence:
            evidence = evidence[:-1]
        pred["rubric_evidence"] = evidence + [
            {"rubric_criterion_key": "spurious_criterion", "status": "supported"}
        ]
    return pred


def build_pair(spec: dict[str, Any], defect: str) -> tuple[dict[str, Any], dict[str, Any]]:
    truth = normalized_spec(spec)
    risky = truth["risky_case"] or defect in ("risky_missed", "risky_recalled")
    safe = truth["safe_to_suggest"] and not risky and defect != "unsafe_suggestion"
    truth = {**truth, "safe_to_suggest": safe, "risky_case": risky}
    prediction = mutated_prediction(truth, defect)
    prediction["suggestion_allowed"] = True if defect == "unsafe_suggestion" else safe
    prediction["human_review_required"] = risky and defect != "risky_missed"
    del prediction["safe_to_suggest"]
    del prediction["risky_case"]
    return truth, prediction


def build_all() -> dict[str, dict[str, Any]]:
    samples: dict[str, dict[str, Any]] = {}
    index = 0
    for category, specs in CATEGORIES.items():
        for number, spec in enumerate(specs, start=1):
            defect = DEFECT_CYCLE[index % len(DEFECT_CYCLE)]
            truth, prediction = build_pair(spec, defect)
            samples[f"synthetic-{category}-{number:03d}"] = {
                "category": category,
                "defect": defect,
                "ground_truth": truth,
                "prediction": prediction,
            }
            index += 1
    return samples


# ---------------------------------------------------------------------------
# Idempotent file emission.
# ---------------------------------------------------------------------------


def dumps_line(payload: Any) -> str:
    return json.dumps(payload, ensure_ascii=False) + "\n"


def write_manifest(path: Path, samples: dict[str, dict[str, Any]]) -> None:
    lines = [line for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    kept: list[str] = []
    for line in lines:
        entry = json.loads(line)
        if entry["sample_id"] == ORIGINAL_ENTRY_ID or entry["sample_id"] not in samples:
            kept.append(line)
    if not kept or json.loads(kept[0])["sample_id"] != ORIGINAL_ENTRY_ID:
        raise ValueError(f"manifest at {path} lost the {ORIGINAL_ENTRY_ID} entry")
    for sample_id in samples:
        kept.append(
            dumps_line(
                {
                    "sample_id": sample_id,
                    "subject_code": SUBJECT_CODE,
                    "original": f"{ORIGINAL_PREFIX}/{sample_id}.png",
                    "ground_truth": f"ground-truth/{sample_id}.json",
                }
            ).rstrip("\n")
        )
    path.write_text("\n".join(kept) + "\n", encoding="utf-8")


# 清理目标目录内所有未列入 keep 的同后缀文件；调用方只能传入专用夹具目录。
def prune_stale(directory: Path, keep: set[str], suffix: str) -> None:
    if not directory.exists():
        return
    for stale in sorted(directory.glob(f"*{suffix}")):
        if stale.stem not in keep:
            stale.unlink()


def generate(fixtures_root: Path) -> dict[str, int]:
    samples = build_all()
    manifest_path = fixtures_root / "manifest.jsonl"
    write_manifest(manifest_path, samples)
    ground_truth_dir = fixtures_root / "ground-truth"
    predictions_dir = fixtures_root / "predictions" / PREDICTION_SET
    ground_truth_dir.mkdir(parents=True, exist_ok=True)
    predictions_dir.mkdir(parents=True, exist_ok=True)
    prune_stale(ground_truth_dir, set(samples) | {ORIGINAL_ENTRY_ID}, ".json")
    prune_stale(predictions_dir, set(samples) | {ORIGINAL_ENTRY_ID}, ".json")
    for sample_id, bundle in samples.items():
        (ground_truth_dir / f"{sample_id}.json").write_text(
            dumps_line(bundle["ground_truth"]), encoding="utf-8"
        )
        (predictions_dir / f"{sample_id}.json").write_text(
            dumps_line(bundle["prediction"]), encoding="utf-8"
        )
    source = fixtures_root / ORIGINAL_PREDICTION_SOURCE
    original_prediction = json.loads(source.read_text(encoding="utf-8"))
    (predictions_dir / f"{ORIGINAL_ENTRY_ID}.json").write_text(
        dumps_line(original_prediction), encoding="utf-8"
    )
    counts = {category: len(specs) for category, specs in CATEGORIES.items()}
    counts["total"] = len(samples)
    return counts


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--fixtures-root",
        type=Path,
        default=Path(__file__).resolve().parent / "fixtures",
    )
    args = parser.parse_args()
    counts = generate(args.fixtures_root)
    for category, count in counts.items():
        print(f"{category}: {count}")
    print(
        "All fixtures are SYNTHETIC; baseline metrics from them are harness "
        "self-checks only, never model quality evidence."
    )


if __name__ == "__main__":
    main()
