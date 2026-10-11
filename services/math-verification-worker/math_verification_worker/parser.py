from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any


class UnsupportedExpression(ValueError):
    pass


# 标识符只接受 ASCII 字母，数字仍接受 Unicode 十进制数；各类按原正则的分支顺序尝试。
COMMAND_TOKEN = re.compile(r"\\frac|\\sqrt|\\cdot|\\times|\\leq?|\\geq?|\\neq")
IDENTIFIER_TOKEN = re.compile(r"[A-Za-z]+")
NUMBER_TOKEN = re.compile(r"\d+(?:\.\d+)?")
SYMBOL_TOKEN = re.compile(r"[+−\-*/^=(){}<>]")
TOKEN_PATTERNS = (COMMAND_TOKEN, IDENTIFIER_TOKEN, NUMBER_TOKEN, SYMBOL_TOKEN)


def _match_token(source: str, position: int) -> re.Match[str] | None:
    for pattern in TOKEN_PATTERNS:
        match = pattern.match(source, position)
        if match:
            return match
    return None


def _tokenize(source: str) -> list[str]:
    tokens: list[str] = []
    position = 0
    while position < len(source):
        match = _match_token(source, position)
        if match:
            tokens.append(match.group())
            position = match.end()
        elif source[position].isspace():
            position += 1
        else:
            # 原词法检查只允许空白残留；非法片段仍先于后面的 token 数量限制报错。
            raise UnsupportedExpression("unsupported construct")
    return tokens


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
        if NUMBER_TOKEN.fullmatch(token):
            return {"kind": "number", "value": token}
        if IDENTIFIER_TOKEN.fullmatch(token):
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
        return bool(token and (IDENTIFIER_TOKEN.fullmatch(token) or NUMBER_TOKEN.fullmatch(token) or token in {"(", "{", "\\frac", "\\sqrt"}))

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
    tokens = _tokenize(compact)
    if len(tokens) > 1024:
        raise UnsupportedExpression("expression is too complex")
    return Parser(tokens).parse()
