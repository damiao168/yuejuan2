import ast
import json
import pathlib
import subprocess
import sys

BASE = pathlib.Path(__file__).parent / "source-20261011/ai-services/grading_agent"
REPORT = pathlib.Path("D:/project/yuejuan/reports/platform-audit-2026-10-10/codeql")


def synthetic_run(size):
    import math
    import re
    import time
    import unicodedata

    environment = {"math": math, "re": re, "unicodedata": unicodedata, "AgentError": RuntimeError}
    requested = {
        "paper_compact/schema.py": {"_PAGE_FOOTER", "_QUESTION_START", "_ANSWER_MARKER", "_SOLUTION_MARKER", "_OPTION", "_DISTRIBUTION_MARKER", "_REPEATED_FURNITURE_MARKER"},
        "paper_compact/layout.py": {"_box"},
        "paper_compact/anchors.py": {"_block_text", "_furniture_signature", "_is_page_furniture"},
        "paper_input.py": {"normalize_paper_input"},
    }
    for relative, names in requested.items():
        parsed = ast.parse((BASE / relative).read_text(encoding="utf-8"))
        nodes = [node for node in parsed.body if isinstance(node, ast.FunctionDef) and node.name in names or isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id in names for target in node.targets)]
        # 只执行已审读的原始纯函数 AST；无包级初始化、模型或接口调用。
        exec(compile(ast.Module(body=nodes, type_ignores=[]), relative, "exec"), environment)
    text = "第1页" + " " * size + "X"
    block = {"text": text, "bbox": [0, 950, 100, 20], "page_no": 1}
    payload = {"request_id": "synthetic-regex-audit", "subject": "math", "documents": [{"source_id": "synthetic-doc", "content": "synthetic educational material", "blocks": [block]}]}
    _, _, cleaned = environment["normalize_paper_input"](payload)
    retained = cleaned[0]["blocks"][0]
    started = time.perf_counter()
    matched = environment["_is_page_furniture"](retained, {1: (0, 1000)}, set())
    return {"padding": size, "block_chars_after_normalize": len(retained["text"]), "is_page_furniture": matched, "seconds": time.perf_counter() - started}


if __name__ == "__main__":
    if len(sys.argv) > 1:
        print(json.dumps(synthetic_run(int(sys.argv[1]))))
    else:
        results = []
        for size in (1024, 2048, 4096, 8192):
            try:
                process = subprocess.run([sys.executable, __file__, str(size)], capture_output=True, text=True, timeout=3)
                if process.returncode:
                    raise RuntimeError(process.stderr)
                results.append(json.loads(process.stdout))
            except subprocess.TimeoutExpired:
                results.append({"padding": size, "timeout_seconds": 3})
                break
        evidence = {"synthetic": True, "scope": "Exact snapshot normalization and page-furniture functions extracted as AST; no server, model, grading, or production request", "results": results}
        (REPORT / "footer-furniture-benchmark.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2), encoding="utf-8")
        print(json.dumps(evidence))
