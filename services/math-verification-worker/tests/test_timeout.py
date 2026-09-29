import json
import multiprocessing
import pickle
import time

import pytest

from math_verification_worker import server
from math_verification_worker.verifier import VerificationError


def test_timeout_returns_unknown():
    children_before = {child.pid for child in multiprocessing.active_children()}
    started = time.monotonic()
    result = server.run_bounded("__test_sleep__", {"seconds": 2}, 0.05)
    assert result["status"] == "uncertain"
    assert result["reason_code"] == "verification_timeout"
    assert time.monotonic() - started < 3
    assert {child.pid for child in multiprocessing.active_children()} == children_before


def _large_ast(count, start=0):
    if count == 1:
        suffix = "".join(chr(97 + (start // (26 ** position)) % 26) for position in range(3))
        return {"kind": "symbol", "value": "x" * 29 + suffix}
    half = count // 2
    return {"kind": "operator", "value": "+", "children": [_large_ast(half, start), _large_ast(count - half, start + half)]}


# 真实 spawn 和超过管道容量的响应共同覆盖 feeder 堵塞，普通小字典不能复现此回归。
def test_spawn_delivers_a_result_larger_than_the_pipe_buffer():
    payload = {"ast": _large_ast(1024)}
    assert len(json.dumps(payload).encode()) < server.MAX_BODY
    expected = server._execute("normalize", payload)
    assert len(pickle.dumps(("ok", expected))) > 128 * 1024
    children_before = {child.pid for child in multiprocessing.active_children()}
    started = time.monotonic()
    result = server.run_bounded("normalize", payload, 15)
    assert result == expected
    assert time.monotonic() - started < 15
    assert {child.pid for child in multiprocessing.active_children()} == children_before


def test_spawn_error_is_reported_and_child_is_reaped():
    children_before = {child.pid for child in multiprocessing.active_children()}
    with pytest.raises(VerificationError, match="unknown operation"):
        server.run_bounded("unsupported", {}, 10)
    assert {child.pid for child in multiprocessing.active_children()} == children_before


def test_worker_remains_usable_after_timeouts():
    children_before = {child.pid for child in multiprocessing.active_children()}
    for _ in range(3):
        result = server.run_bounded("__test_sleep__", {"seconds": 2}, 0.01)
        assert result["reason_code"] == "verification_timeout"
    assert server.run_bounded("normalize", {"latex": "0.1+0.2"}, 10)["canonical"] == "3/10"
    assert {child.pid for child in multiprocessing.active_children()} == children_before
