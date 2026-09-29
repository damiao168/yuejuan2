"""Read-only micro reproductions; uses actual verifier, no models or services."""
from __future__ import annotations

import json
import multiprocessing
import pickle
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "services/math-verification-worker"))

from math_verification_worker.parser import parse_restricted_latex as parse
from math_verification_worker.server import _execute, _process_entry, run_bounded
from math_verification_worker.verifier import solve, verify_equivalence, verify_transition


def emit(case: str, observed: object, expected: str) -> None:
    print(json.dumps({"case": case, "observed": observed, "expected": expected}, ensure_ascii=False), flush=True)


def main() -> None:
    emit("integer_solve", solve(parse("x^2=2"), "x", {"variables": {"x": "integer"}}), "EmptySet")
    emit("positive_real_transition", verify_transition(parse("x^2=4"), parse("x=2"), {"variables": {"x": "positive_real"}}).__dict__, "verified equivalent_transform")
    emit("zero_denominator", verify_transition(parse("1/0"), parse("2/0")).__dict__, "must not be verified: both expressions are undefined over reals")
    emit("negative_radical", verify_transition(parse(r"\sqrt{0-1}"), parse(r"2\sqrt{0-1}/2")).__dict__, "must not be verified in real domain")
    emit("decimal_identity", verify_equivalence(parse("0.1x+0.2x"), parse("0.3x")).__dict__, "verified")
    # Balanced valid direct AST keeps recursion bounded while producing a large
    # normalization result. This does not establish reachability through LaTeX.
    def tree(n: int, start: int = 0) -> dict:
        if n == 1:
            suffix = "".join(chr(97 + (start // (26 ** position)) % 26) for position in range(3))
            return {"kind": "symbol", "value": "x" * 29 + suffix}
        return {"kind": "operator", "value": "+", "children": [tree(n // 2, start), tree(n - n // 2, start + n // 2)]}
    for count in (4, 512):
        payload = {"ast": tree(count)}
        start = time.monotonic()
        direct = _execute("normalize", payload)
        direct_ms = round((time.monotonic() - start) * 1000, 2)
        start = time.monotonic()
        bounded = run_bounded("normalize", payload, 15.0)
        elapsed_ms = round((time.monotonic() - start) * 1000, 2)
        emit(f"queue_result_{count}", {
            "request_bytes": len(json.dumps(payload).encode()),
            "pickled_response_bytes": len(pickle.dumps(("ok", direct))),
            "direct_ms": direct_ms,
            "direct_canonical_length": len(direct.get("canonical", "")),
            "bounded_ms": elapsed_ms,
            "bounded_matches_direct": bounded == direct,
            "bounded_status": bounded.get("status"),
            "bounded_reason": bounded.get("reason_code"),
        }, "bounded result must equal direct result")
        if count == 512:
            # Control: same production child target and payload, but drain the
            # queue before join, allowing the child feeder thread to finish.
            queue = multiprocessing.Queue(maxsize=1)
            process = multiprocessing.Process(target=_process_entry, args=(queue, "normalize", payload), daemon=True)
            start = time.monotonic()
            process.start()
            item = queue.get(timeout=30)
            process.join(5)
            emit("queue_drain_before_join", {"matches_direct": item == ("ok", direct), "child_exited": not process.is_alive(), "elapsed_ms": round((time.monotonic() - start) * 1000, 2)}, "same result succeeds when queue is drained before process join")


if __name__ == "__main__":
    main()
