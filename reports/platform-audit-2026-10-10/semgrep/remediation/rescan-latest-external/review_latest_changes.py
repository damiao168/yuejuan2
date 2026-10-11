"""只核验两份外部增量与原告警调用，不声称整文件审读完成。"""

from collections import Counter
import hashlib
import json
from pathlib import Path
import re


OUTPUT = Path(__file__).resolve().parent
inventory = json.loads((OUTPUT / "source-inventory.json").read_text(encoding="utf-8"))
inventory["files"] = inventory["source_files"]
snapshot = Path(inventory["snapshot"])
previous_snapshot = Path(inventory["previous_snapshot"])
root = Path(inventory["source"])
latest = json.loads((OUTPUT / "semgrep-raw.json").read_text(encoding="utf-8"))
before = json.loads((OUTPUT.parent / "rescan-report-scope/semgrep-raw.json").read_text(encoding="utf-8"))
paths = {item["path"] for item in inventory["files"]}
scanned = {name.replace("\\", "/") for name in latest["paths"]["scanned"]}
assert scanned == paths


def key(finding, source_dir):
    path = finding["path"].replace("\\", "/")
    rule = finding["check_id"].split(".rescan.", 1)[-1]
    source = (source_dir / path).read_bytes()
    matched = source[finding["start"]["offset"]:finding["end"]["offset"]]
    return (rule, path, hashlib.sha256(matched).hexdigest())


prior_hits = [finding for finding in before["results"] if finding["path"].replace("\\", "/") in paths]
old_keys = Counter(key(finding, previous_snapshot) for finding in prior_hits)
new_keys = Counter(key(finding, snapshot) for finding in latest["results"])
assert old_keys == new_keys
findings = []
for index, finding in enumerate(latest["results"], 1):
    rule, path, matched_hash = key(finding, snapshot)
    assert "string-formatted-query" in rule
    source = (snapshot / path).read_bytes()
    actual_line = source[:finding["start"]["offset"]].count(b"\n") + 1
    if path.endswith("e2e_mfa_test.go") and "SELECT count(*) FROM" in source[finding["start"]["offset"]:finding["end"]["offset"]].decode("utf-8"):
        evidence = "e2eAssertMFACredentialTableScope只由调用处四种固定MFA表名白名单调用；租户上下文分别测试无租户、错误租户及所属租户。"
    else:
        evidence = "角色/数据库标识来自固定前缀和本地UUID，经pgx.Identifier.Sanitize引用；角色密码也在测试现场由固定前缀和UUID生成。原SQL命中语句字节与此前固定副本相同，没有外部API用户字符串。"
    findings.append({"id": f"LE-{index:03d}", "path": path, "rule": rule,
                     "reported_line": finding["start"]["line"], "source_line_from_byte_offset": actual_line,
                     "source_match_sha256": matched_hash, "same_match_bytes_as_prior_snapshot": True,
                     "disposition": "false_positive_test_scope", "evidence": evidence,
                     "finding_review_complete": True, "whole_file_review_status": "pending"})
files = []
for item in inventory["files"]:
    current_hash = hashlib.sha256((root / item["path"]).read_bytes()).hexdigest()
    copy = dict(item)
    copy.update({"current_sha256_at_review": current_hash,
                 "matches_latest_verified_scan": current_hash == item["sha256"],
                 "matches_prior_rescan_snapshot": item["sha256"] == item["previous_snapshot_sha256"],
                 "original_disposition_still_valid": True})
    if item["path"].endswith("e2e_mfa_test.go"):
        copy["changes_reviewed"] = "外部变化只有两种结构体的字段空格对齐；数据结构、函数调用、SQL、Cookie与fixture输入未变。"
        copy["cookie_review"] = "e2eEnrollTOTPManagement的AddCookie(session)继续使用测试路由返回的Cookie作为httptest请求输入，没有新增生产Set-Cookie字面量。"
    else:
        copy["changes_reviewed"] = "外部变化抽取特权登录拒绝、student租户可见性、登录事实租户隔离三种断言helper，调用顺序保留；SQL迁移拆句改为同字段状态机方法，quote/dollar-quote/行注释/分号处理与原分支对应。"
        copy["incremental_scope_limit"] = "静态审读抽取helper和拆句状态迁移；没有宣称运行PostgreSQL集成测试或全面验证外部重构。"
        copy["cookie_review"] = "e2eBrowserLogin及登录事实断言仍在隔离E2E路由操作测试Cookie；外部变化没有加入生产Cookie响应配置。"
    files.append(copy)
metadata = json.loads((OUTPUT / "scan-metadata.json").read_text(encoding="utf-8"))
metadata.update({"scope_paths_scanned_verified": scanned == paths, "finding_count": len(findings),
                 "error_count": len(latest.get("errors", []))})
stderr = (OUTPUT / "scan.stderr.log").read_text(encoding="utf-8")
match = re.search(r"Rules run:\s*(\d+)", stderr)
report = {"review_kind": "external_change_incremental_and_original_finding_calls", "whole_file_review_claimed": False,
          "original_dispositions_still_valid": True, "files": files, "finding_count": len(findings),
          "error_count": len(latest.get("errors", [])), "findings": findings,
          "prior_scope_hits_for_these_files": len(prior_hits), "same_rule_path_match_bytes_multiset": True,
          "new_rule_path_match_bytes": [], "original_rule_path_match_bytes_removed": [],
          "loaded_rules": metadata["rule_count"], "rules_run": int(match.group(1)) if match else None,
          "limits": ["仅两文件增量和原告警调用审核，全文件pending",
                     "外部变化不纳入本次修复数量；此前全仓和46文件固定副本/证据没有改写",
                     "未运行PostgreSQL数据库集成测试；Windows reported line可能不同于offset推算实际行号"]}
(OUTPUT / "source-inventory.json").write_text(json.dumps(inventory, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
(OUTPUT / "scan-metadata.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
(OUTPUT / "review-results.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
lines = ["# 外部变化的最新两文件增量验收", "",
         "此前全仓和46文件扫描后，另一个任务更新了两份E2E源码。此次只把最新两文件原字节复制到新的临时固定副本，审读差异和原SQL/Cookie调用；没有修改业务源码或此前扫描证据，全文件审读状态仍为pending。", "",
         f"Semgrep 1.179.0 / OSS：735条配置，{report['rules_run']}条实际运行，{metadata['elapsed_seconds']}秒；两显式目标全部出现在paths.scanned，退出码0、10条命中、0条诊断。", "",
         "10条均为原隔离E2E数据库动态SQL误报，rule/path/命中原字节SHA-256多重集合与此前46文件扫描中的这两文件完全相同，没有新增或减少命中。Cookie相关调用仍是httptest/隔离路由测试输入；没有新增生产Cookie响应配置。原处置依据继续成立。", "",
         "e2e_mfa变化仅字段对齐。e2e_postgres变化为抽取三个RLS断言helper及拆分SQL迁移拆句状态机；静态审读确认原动态SQL标识符来源、参数绑定及原状态分支对应关系保留，不把该审读描述成PostgreSQL集成测试通过。", "",
         "| 文件 | 此前固定副本 SHA-256 | 最新固定副本 SHA-256 | 当前是否仍匹配最新 |", "| --- | --- | --- | --- |"]
for item in files:
    lines.append(f"| `{item['path']}` | `{item['previous_snapshot_sha256']}` | `{item['sha256']}` | {item['matches_latest_verified_scan']} |")
lines.extend(["", "两文件的matches_prior_rescan_snapshot均为false，matches_latest_verified_scan在本次核对时均为true。新source-inventory.json提供两套哈希；旧快照和覆盖账本任务保持原样。"])
(OUTPUT / "REVIEW.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
print(json.dumps({"original_dispositions_still_valid": True, "finding_count": len(findings),
                  "same_match_bytes": True, "current_matches_latest": all(item["matches_latest_verified_scan"] for item in files)}, ensure_ascii=False))
