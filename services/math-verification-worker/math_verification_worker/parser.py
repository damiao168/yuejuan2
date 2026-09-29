from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any


class UnsupportedExpression(ValueError):
    pass


TOKEN = re.compile(r"\\frac|\\sqrt|\\cdot|\\times|\\leq?|\\geq?|\\neq|[A-Za-z]+|\d+(?:\.\d+)?|[+−\-*/^=(){}<>]")


@dataclass
class Parser:
    tokens: list[str]
    position: int = 0

    def parse(self) -> dict[str, Any]:
        left = self.expression()
        if self.peek() in {"=", "<", ">", "\\le", "\\leq", "\\ge", "\\geq", "\\neq"}:
            operator = self.take()
            right = self.expression()
            kind = "equation" if operator == "=" else "inequality"
            result = {"kind": kind, "value": operator, "children": [left, right]}
        else:
            result = left
        if self.position != len(self.tokens):
            raise UnsupportedExpression("unsupported trailing expression")
        return result

    def expression(self) -> dict[str, Any]:
        node = self.term()
        while self.peek() in {"+", "-", "−"}:
            operator = self.take().replace("−", "-")
            node = {"kind": "operator", "value": operator, "children": [node, self.term()]}
        return node

    def term(self) -> dict[str, Any]:
        node = self.power()
        while self.peek() in {"*", "/", "\\cdot", "\\times"} or self._implicit_product():
            operator = "*" if self._implicit_product() else self.take()
            right = self.power()
            if operator == "/":
                node = {"kind": "fraction", "children": [node, right]}
            else:
                node = {"kind": "operator", "value": "*", "children": [node, right]}
        return node

    def power(self) -> dict[str, Any]:
        node = self.atom()
        if self.peek() == "^":
            self.take()
            node = {"kind": "power", "children": [node, self.atom()]}
        return node

    def atom(self) -> dict[str, Any]:
        token = self.take()
        if token == "\\frac":
            return {"kind": "fraction", "children": [self.group(), self.group()]}
        if token == "\\sqrt":
            return {"kind": "radical", "children": [self.group()]}
        if token in {"(", "{"}:
            close = ")" if token == "(" else "}"
            node = self.expression()
            if self.take() != close:
                raise UnsupportedExpression("unbalanced group")
            return {"kind": "group", "children": [node]}
        if re.fullmatch(r"\d+(?:\.\d+)?", token):
            return {"kind": "number", "value": token}
        if re.fullmatch(r"[A-Za-z]+", token):
            if self.peek() == "(" and token in {"sin", "cos", "tan", "abs"}:
                self.take()
                argument = self.expression()
                if self.take() != ")":
                    raise UnsupportedExpression("unbalanced function")
                return {"kind": "function", "value": token, "children": [argument]}
            return {"kind": "symbol", "value": token}
        raise UnsupportedExpression(f"unsupported token: {token}")

    def group(self) -> dict[str, Any]:
        opener = self.take()
        if opener not in {"{", "("}:
            raise UnsupportedExpression("expected group")
        closer = "}" if opener == "{" else ")"
        node = self.expression()
        if self.take() != closer:
            raise UnsupportedExpression("unbalanced group")
        return node

    def _implicit_product(self) -> bool:
        token = self.peek()
        return bool(token and (re.fullmatch(r"[A-Za-z]+|\d+(?:\.\d+)?", token) or token in {"(", "{", "\\frac", "\\sqrt"}))

    def peek(self) -> str:
        return self.tokens[self.position] if self.position < len(self.tokens) else ""

    def take(self) -> str:
        if self.position >= len(self.tokens):
            raise UnsupportedExpression("unexpected end of expression")
        value = self.tokens[self.position]
        self.position += 1
        return value


def parse_restricted_latex(source: str) -> dict[str, Any]:
    compact = source.strip().replace("\\left", "").replace("\\right", "")
    if not compact or len(compact) > 4096:
        raise UnsupportedExpression("empty or oversized expression")
    # 必须拒绝词法表之外的残留，不能跳过无法识别的公式片段后继续认证。
    tokens = TOKEN.findall(compact)
    residue = TOKEN.sub("", compact)
    if residue.strip():
        raise UnsupportedExpression("unsupported construct")
    if len(tokens) > 1024:
        raise UnsupportedExpression("expression is too complex")
    return Parser(tokens).parse()
