"""记录指定 Go 告警的实际人工判定；固定快照源码不作改动。"""
from __future__ import annotations

import csv
import hashlib
import json
from collections import Counter
from datetime import datetime, timedelta, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
SNAPSHOT = Path("D:/tools/audit-codeql-20261010/source-20261011")
MANIFEST = HERE.parent / "source-manifest-resume-2026-10-11.csv"


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def lines(path, first, last):
    source = path.read_text(encoding="utf-8").splitlines()
    return [f"{i + 1}: {source[i]}" for i in range(first - 1, min(last, len(source)))]


with MANIFEST.open(encoding="utf-8-sig", newline="") as stream:
    hashes = {row["path"]: row["sha256"] for row in csv.DictReader(stream)}
run = json.loads((HERE / "go.sarif").read_text(encoding="utf-8"))["runs"][0]
rules = {row["id"]: row for row in run["tool"]["driver"]["rules"]}
base = "services/api-gateway/internal/"

score_reason = (
    "告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，"
    "直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。"
    "数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。"
    "测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。"
)
score_fix = "本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。"

choices = {
    0: (
        "false_positive_context", "none",
        "test.content 的 json.Marshal 返回完整且已转义的 JSON 字符串 token，"
        "模板在 content 冒号后直接拼入 token，没有额外包引号。"
        "finishReason 来自本例三个固定字段 stop/length，其他源码路径不是本条 source。"
        "此处有意测试非法/截断的内层模型回答；外层响应仍正确 JSON 编码。",
        "保留模型能力失败闭合的测试场景；可改整体外层结构编码以减少扫描歧义，不删除 json.Marshal 转义。",
        [(base + "modelgovernance/managed_config_probe_http_test.go", 374, 415)],
    ),
    1: ("false_positive_context", "none", score_reason, score_fix, [(base + "paper/handlers_test.go", 518, 533)]),
    2: ("false_positive_context", "none", score_reason, score_fix, [(base + "paper/handlers_test.go", 518, 533)]),
    3: ("false_positive_context", "none", score_reason, score_fix, [(base + "paper/handlers_test.go", 526, 533)]),
    4: ("false_positive_context", "none", score_reason, score_fix, [(base + "paper/handlers_test.go", 526, 533)]),
    5: (
        "false_positive_context", "none",
        "qbJSON 检查 json.Marshal 错误后返回完整 JSON token。该告警参数 v.DefaultScore 在类型中为 float64，"
        "模板把结果直接放在 total_score 冒号后，不是放进双引号字符串。"
        "因此不是引号逃逸；同一模板其他 fixture ID 拼接未包含在本条数据流判断内，不能以本条误报推断整个模板适用于任意外部输入。",
        "该候选记录为上下文误报。测试模板可在后续统一为整体 JSON 编码以提高维护性。",
        [(base + "server/e2e_question_bank_import_test.go", 196, 216), (base + "server/e2e_question_bank_test.go", 317, 324), (base + "questionbank/types.go", 62, 80)],
    ),
    7: (
        "intentional_test_behavior", "none",
        "TestNewClientRejectsTLSBeforeVersion12 有意构造最大 TLS1.1 的 httptest 服务，"
        "断言真实 NewClient 客户端在发送 HTTP 前拒绝该服务；"
        "生产 MinIO NewClient 的 cfg.UseSSL 分支设置 TLSClientConfig.MinVersion = TLS1.2。"
        "不是业务客户端允许 TLS1.1，也不是只按 _test.go 文件名排除。",
        "保留低协议版本拒绝测试，针对本条记录负例用途。实际运行部署和用例执行结果未在此次人工核验中验证。",
        [(base + "minioadapter/client_test.go", 68, 98), (base + "minioadapter/client.go", 15, 47)],
    ),
    16: (
        "false_positive_context", "none",
        "drawQRCode 故意让 columnIndex 到 len(row) 的哨兵位，以冲刷最后一段黑色模块。"
        "唯一 row[columnIndex] 读取位于 `columnIndex < len(row) && row[columnIndex]` 右侧，"
        "Go && 短路保证哨兵位不会执行读取；range 提供实际行，列索引从0递增。"
        "后续只用列差计算绘图矩形，无第二次 bitmap 索引；外层空 bitmap 也已提前返回。"
        "不需依赖 qrcode.Bitmap 保证方阵才能排除这条越界，空行同样不读取。",
        "保留末尾哨兵及读取前的严格小于 guard；后续可补说明用于冲刷末尾黑段。不能仅把 <= 改成 <，否则会漏画以黑色结束的 run。",
        [(base + "capture/print_package_pdf.go", 69, 98), (base + "capture/print_package_pdf.go", 133, 158)],
    ),
    17: (
        "low_maintenance", "low",
        "首次 UpdatePolicy 创建的 current.TenantID 在此分支内赋值后不读取；最终新 policy 独立设置 TenantID=tenantID，并保存到 s.policies[tenantID]。"
        "这处确为不必要的初值写入，不是内存策略丢失租户隔离的证据。current.ID/Version/DisplayName 则有后续用途，不能整体删掉初始化分支。",
        "后续仅移除无读用途的 current.TenantID 初始化，保留 policy.TenantID、版本校验和映射键。",
        [(base + "modelgovernance/store_memory.go", 280, 323)],
    ),
    18: (
        "low_maintenance", "low",
        "current.PolicyKey 的首次初始化未在后续被读取，新 policy.PolicyKey 直接设置 default；"
        "current 的整个结构没有在此分支写回 map。该告警对应冗余字段写入，不导致策略默认键缺失。",
        "后续可移除 current.PolicyKey 的冗余初值，保留最终 policy.PolicyKey。",
        [(base + "modelgovernance/store_memory.go", 299, 323)],
    ),
    19: (
        "low_maintenance", "low",
        "current.Status 的首次初始化没有读取路径，新 policy.Status 随后直接设 active 并保存。"
        "赋值本身无副作用，属于维护冗余；不据此认定状态验证或乐观锁失效。",
        "后续可移除该 current.Status 冗余赋值，保留最终 policy.Status 及既有政策校验。",
        [(base + "modelgovernance/store_memory.go", 280, 323)],
    ),
    20: (
        "low_maintenance", "low",
        "撤权 UpdateACL 的返回 doc 被重新赋给局部变量后不再使用；旧 doc.Revision 在调用实参中仍被读取。"
        "调用执行撤权副作用，后续实际查询验证撤权，err 也有检查。仅返回新值无用途，不是撤权调用无效。",
        "后续可将该调用的新 doc 返回值赋给 _；必须保留变更调用、ExpectedRevision 和错误/撤权验证。",
        [(base + "questionbank/metadata_acl_search_test.go", 95, 133)],
    ),
    21: (
        "low_maintenance", "low",
        "最后一次 UpdateACL 返回的 updated 不再读取；调用前的旧 updated.Revision 用于乐观版本检查。"
        "后续数据库查询检查管理者 direct action 和被移除组 edit 权限，调用副作用必须执行。",
        "后续可把这个返回对象接到 _，保留 err 和数据库断言；不能删除整个调用。",
        [(base + "server/e2e_question_bank_acl_ux_test.go", 172, 204)],
    ),
    22: (
        "low_maintenance", "low",
        "再授权 UpdateACL 的 acl 返回值不在后续使用；旧 acl.Revision 已在同一调用实参消费。"
        "授权后必须执行 RetireItem，后面的审计记录和历史 schema 保护也仍验证行为。",
        "后续可只丢弃返回对象；必须保留再授权动作、ExpectedRevision、err 和后续权限/审计断言。",
        [(base + "server/e2e_question_bank_metadata_acl_search_test.go", 140, 169)],
    ),
}

findings = []
for index, (verdict, priority, reason, recommendation, support) in choices.items():
    result = run["results"][index]
    rule = rules[result["ruleId"]]
    location = result["locations"][0]["physicalLocation"]
    path = location["artifactLocation"]["uri"]
    props = rule.get("properties", {})
    source_hash = sha(SNAPSHOT / path)
    assert source_hash == hashes[path], path
    evidence = []
    for file, first, last in support:
        snapshot_file = SNAPSHOT / file
        file_hash = sha(snapshot_file)
        assert file_hash == hashes[file], file
        evidence.append({"path": file, "start_line": first, "end_line": last, "snapshot_sha256": file_hash, "lines": lines(snapshot_file, first, last)})
    findings.append({
        "review_id": f"CQ-GO-{index + 1:03}", "sarif_result_index_zero_based": index,
        "rule_id": result["ruleId"], "path": path, "line": location["region"]["startLine"],
        "raw_level": result.get("level"), "raw_rule_default_level": rule.get("defaultConfiguration", {}).get("level"),
        "raw_problem_severity": props.get("problem.severity"),
        "effective_problem_level": result.get("level") or rule.get("defaultConfiguration", {}).get("level") or props.get("problem.severity"),
        "effective_level_derivation": "result.level, then rule.defaultConfiguration.level, then rule properties.problem.severity; original fields retained",
        "raw_security_severity": props.get("security-severity"), "raw_message": result["message"]["text"],
        "security_finding": "security" in props.get("tags", []),
        "manual_review_status": "reviewed_alert_context", "human_verdict": verdict,
        "human_severity": priority, "reason": reason, "notes": reason,
        "recommendation": recommendation, "evidence": evidence, "snapshot_sha256": source_hash,
        "current_workspace_used_for_verdict": False,
    })

assert len(findings) == 14
reviewed_at = datetime.now(timezone(timedelta(hours=8))).isoformat()
value = {
    "reviewed_at": reviewed_at, "engine": "CodeQL CLI 2.27.2, Go security-and-quality 59 queries",
    "source_root": str(SNAPSHOT), "sarif_sha256": sha(HERE / "go.sarif"),
    "manifest_sha256": sha(MANIFEST), "raw_go_results_total": len(run["results"]),
    "assigned_sarif_result_indices_zero_based": list(choices), "assigned_results_reviewed": 14,
    "assigned_results_pending": 0, "finding_reviews_pending": 0,
    "security_results_in_this_part": sum(f["security_finding"] for f in findings),
    "quality_results_in_this_part": sum(not f["security_finding"] for f in findings),
    "human_verdict_counts": dict(Counter(f["human_verdict"] for f in findings)),
    "confirmed_business_runtime_defects_in_reviewed_results": 0,
    "scope": "Only assigned 14 alert contexts reviewed; remaining 9 alerts are reviewed by root separately. Not whole-file/full-repository manual completion.",
    "source_validation": "Alert files and listed support contexts exactly match fixed snapshot SHA256 manifest.",
    "tests_executed_in_this_review": False, "business_sources_modified": False,
    "findings": findings,
}
(HERE / "go-review-part-quality.json").write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
md = ["# Go CodeQL 指定 14 条候选核验", "", f"核验时间：{reviewed_at}。原始 [go.sarif](go.sarif) SHA-256：`{value['sarif_sha256']}`。", "", "本附录覆盖零基索引 0..5、7、16、17..22，共 14 条：7 条上下文误报、1 条有意的 TLS 低版本拒绝负例、6 条低优先级冗余赋值维护。其他 9 条由主智能体核验，须合并后才能称该 Go SARIF 的全部 23 条人工结论完整。", "", "只依据 2026-10-11 固定本地副本；每个告警和支持调用链源哈希与扫描清单一致。读告警所在测试不是排除标准：仍实际检查 JSON 编码/嵌入位置、TLS 客户端限制、变更副作用、版本约束与错误断言。此次未运行这些测试或生产服务；对源码边界的解释不等于部署验证。", "", "原始 result.level 缺省为 null 时保留 null，并按 SARIF rule 默认等级及 problem.severity 派生展示等级；不会把 `error` 自动当作安全漏洞。7 条安全标签保留原平台分数（6 个 unsafe-quoting 9.3、1 个 insecure-tls 7.5），人工解释另列。", "", "drawQRCode 的越界候选有明确 `columnIndex < len(row) && row[columnIndex]` 短路保护，尾哨兵用于画出结束于黑色的最后一段。修改循环边界而删除尾哨兵会破坏绘图，不能照着告警直接修。", ""]
for f in findings:
    md += [f"## {f['review_id']} · {f['rule_id']}", "", f"[{f['path']}:{f['line']}](<{SNAPSHOT.as_posix()}/{f['path']}:{f['line']}>)", "", f"原始 problem level `{f['effective_problem_level']}`，security severity `{f['raw_security_severity'] or '无'}`；人工 `{f['human_verdict']}` / `{f['human_severity']}`。", "", f["reason"], "", f"建议：{f['recommendation']}", "", f"固定源 SHA-256：`{f['snapshot_sha256']}`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。", ""]
(HERE / "go-review-part-quality.md").write_text("\n".join(md), encoding="utf-8")
print(json.dumps({"assigned_reviewed": len(findings), "verdicts": value["human_verdict_counts"], "pending_in_assigned": 0}, ensure_ascii=False))
