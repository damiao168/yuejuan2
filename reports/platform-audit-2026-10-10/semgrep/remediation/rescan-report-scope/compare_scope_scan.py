"""按规则与仓库路径比较报告范围，保留扫描差异与解析限制。"""

from collections import Counter
import hashlib
import json
from pathlib import Path
import re


OUTPUT = Path(__file__).resolve().parent
AUDIT = OUTPUT.parents[1]
ROOT = AUDIT.parents[2]
scope = json.loads((OUTPUT / "scope.json").read_text(encoding="utf-8"))
snapshot = Path(scope["snapshot"])
original = json.loads((AUDIT / "findings-triage.json").read_text(encoding="utf-8"))
result = json.loads((OUTPUT / "semgrep-raw.json").read_text(encoding="utf-8"))
metadata = json.loads((OUTPUT / "scan-metadata.json").read_text(encoding="utf-8"))


def rule_name(rule: str) -> str:
    return rule.split(".rescan.", 1)[1] if ".rescan." in rule else rule.removeprefix("audit.")


before = Counter((rule_name(finding["rule"]), finding["path"]) for finding in original)
after = Counter((rule_name(finding["check_id"]), finding["path"].replace("\\", "/"))
                for finding in result["results"])
scanned = {name.replace("\\", "/") for name in result["paths"]["scanned"]}
expected = set(scope["paths"])
assert scanned == expected
assert {finding["path"] for finding in original} <= scanned
coverage = []
for name in scope["paths"]:
    source = snapshot / name
    current = ROOT / name
    digest = hashlib.sha256(source.read_bytes()).hexdigest()
    current_digest = hashlib.sha256(current.read_bytes()).hexdigest() if current.is_file() else None
    coverage.append({"path": name, "scanned": name in scanned, "snapshot_sha256": digest,
                     "current_sha256": current_digest, "snapshot_matches_current": digest == current_digest})
removed = []
for (rule, path), count in (before - after).items():
    removed.append({"rule": rule, "path": path, "count_difference": count,
                    "original_ids": [finding["id"] for finding in original
                                     if finding["path"] == path and rule_name(finding["rule"]) == rule]})
new = []
notes = {
    "ai-services/tests/test_model_transport_security.py":
        "OpenSSL 可执行文件仅来自本地测试环境变量或 shutil.which，要求绝对路径并 strict resolve。"
        "测试用固定 req 参数创建随机临时证书，使用参数数组且不启用 shell；未见生产 API 用户输入。",
    "infra/docker-compose/nginx/tests/check_proxy_authority.py":
        "Nginx 可执行文件来自测试者显式 --nginx 本地路径并 resolve；规则命中固定 -v 版本查询。"
        "subprocess 参数为数组且不启用 shell；这是运行测试者主动选择的本地工具，未见生产 API 用户输入。",
}
for (rule, path), count in (after - before).items():
    matches = [finding for finding in result["results"]
               if finding["path"].replace("\\", "/") == path and rule_name(finding["check_id"]) == rule]
    new.append({"rule": rule, "path": path, "count_difference": count,
                "lines": [finding["start"]["line"] for finding in matches],
                "triage": "trusted_local_test_tool", "notes": notes.get(path, "未审读")})
stderr = (OUTPUT / "scan.stderr.log").read_text(encoding="utf-8")
run_count = re.search(r"Rules run:\s*(\d+)", stderr)
diagnostics = [{"path": diagnostic.get("path", "").replace("\\", "/"),
                "type": diagnostic.get("type"), "message": diagnostic.get("message")}
               for diagnostic in result.get("errors", [])]
report = {
    "scan_kind": "report_scope_fixed_snapshot", "full_repository_rescan": False,
    "version": result["version"], "engine": "OSS", "loaded_rules": metadata["rule_count"],
    "rules_run": int(run_count.group(1)) if run_count else None,
    "elapsed_seconds": metadata["elapsed_seconds"], "exit_code": metadata["exit_code"],
    "targets_requested": len(expected), "targets_scanned": len(scanned), "coverage_complete_for_scope": True,
    "original_finding_files": len({finding["path"] for finding in original}), "original_files_unscanned": [],
    "findings_current": len(result["results"]), "original_findings": len(original),
    "rule_path_count_retained": sum((before & after).values()),
    "rule_path_count_disappeared": sum((before - after).values()),
    "rule_path_count_new_or_increased": sum((after - before).values()),
    "rule_counts": dict(Counter(rule_name(finding["check_id"]) for finding in result["results"])),
    "files": coverage, "disappeared_rule_path_counts": removed, "new_rule_path_counts": new,
    "diagnostic_count": len(diagnostics), "diagnostics": diagnostics,
    "interpretation_limits": [
        "规则命中计数不是漏洞数量，也不是修复数量；rule/path 对比不声称每个旧行号已一对一映射",
        "两个 E2E Dockerfile 有 PartialParsing；SG-091 警告区域覆盖最终 USER，消失不能作为运行身份保证",
        "生产 Nginx 六条 header-redefinition 消失，但原重复响应头保留，属于匹配差异，不能计为六处漏洞修复",
        "OCR Dockerfile PartialParsing 与 root 告警仍在；最终 USER nobody 需源代码和运行测试分别核验",
        "Windows 定向扫描覆盖全部46目标，但不代表跨平台解析一致或全仓覆盖",
    ],
}
(OUTPUT / "comparison.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
metadata["network_isolation"] = False
metadata["cloud_upload"] = "not_requested"
metadata["scope_paths_scanned_verified"] = True
(OUTPUT / "scan-metadata.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({key: report[key] for key in ("targets_scanned", "coverage_complete_for_scope", "findings_current",
                                               "rule_path_count_retained", "rule_path_count_disappeared",
                                               "rule_path_count_new_or_increased", "diagnostic_count")}, ensure_ascii=False))
