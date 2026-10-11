import ast
import hashlib
import json
import pathlib
import subprocess
import sys

SOURCE = pathlib.Path(__file__).parent / "source-20261011/ai-services/grading_agent/paper_compact/schema.py"
OUTPUT = pathlib.Path("D:/project/yuejuan/reports/platform-audit-2026-10-10/codeql/footer-regex-benchmark.json")


if __name__ == "__main__":
    module = ast.parse(SOURCE.read_text(encoding="utf-8"))
    pattern = next(ast.literal_eval(node.value.args[0]) for node in module.body if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id == "_PAGE_FOOTER" for target in node.targets))
    results = []
    script = "import json,re,time,sys; p=re.compile(sys.argv[1]); n=int(sys.argv[2]); x='第1页'+' '*n+'X'; t=time.perf_counter(); v=p.fullmatch(x.strip()); print(json.dumps({'padding':n,'input_chars':len(x),'matched':bool(v),'seconds':time.perf_counter()-t}))"
    # 只在隔离短进程中使用合成字符串；每次严格超时，不请求业务接口。
    for size in (256, 512, 1024, 2048, 4096, 8192, 16384):
        try:
            process = subprocess.run([sys.executable, "-c", script, pattern, str(size)], capture_output=True, text=True, timeout=2)
            results.append(json.loads(process.stdout))
        except subprocess.TimeoutExpired:
            results.append({"padding": size, "timeout_seconds": 2})
            break
    evidence = {"synthetic": True, "snapshot_schema_sha256": hashlib.sha256(SOURCE.read_bytes()).hexdigest(), "python_version": sys.version, "pattern_source": str(SOURCE), "results": results, "scope": "Single original compiled regex on synthetic strings, no production request or full pipeline exercised"}
    OUTPUT.write_text(json.dumps(evidence, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(evidence, ensure_ascii=False))
