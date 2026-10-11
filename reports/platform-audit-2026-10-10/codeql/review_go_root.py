"""保留官方等级，按固定扫描源码记录指定 Go 告警的上下文核验。"""
from collections import Counter
from datetime import datetime, timedelta, timezone
import csv
import hashlib
import json
from pathlib import Path

REPORT = Path(__file__).resolve().parent
SOURCE = Path(r"D:\tools\audit-codeql-20261010\source-20261011")
PREFIX = "services/api-gateway/internal/"
MANIFEST = REPORT.parent / "source-manifest-resume-2026-10-11.csv"


def main():
    raw = json.loads((REPORT / "go.sarif").read_text(encoding="utf-8"))
    run = raw["runs"][0]
    rules = {r["id"]: r for r in run["tool"]["driver"]["rules"]}
    with MANIFEST.open(encoding="utf-8-sig", newline="") as f:
        hashes = {r["path"]: r["sha256"] for r in csv.DictReader(f)}

    def evidence(relative, start, end):
        path = PREFIX + relative
        data = (SOURCE / path).read_bytes()
        sha = hashlib.sha256(data).hexdigest()
        assert sha == hashes[path], path
        rows = data.decode("utf-8").splitlines()
        return {"path": path, "start_line": start, "end_line": end,
                "snapshot_sha256": sha,
                "lines": [f"{i}: {rows[i-1]}" for i in range(start, min(end, len(rows)) + 1)]}

    # 以下只记录实际审读的告警和支持上下文，不把文件清单改为全文件已审读。
    cases = [
        (6, "low_hardening", "low",
         "官方报告容量加法可能溢出。唯一调用处先把新增 checks 数量限制为 1..2048；已有 Verifications 是已经分配的非零大小结构切片，不能据此证明可让 len 接近 int 上限。HTTP JSON 入口另有 2 MiB 上限。未复现整数溢出或运行 panic；但已有验证列表的校验函数没有独立条目数上限，因此保留资源预算加固建议，不宣称所有输入规模已经有统一上限。",
         "为已有与合并后的验证记录设定明确数量预算，在容量相加前检查预算；覆盖重复修订、语法记录保留与合法符号验证场景。",
         [("mathunderstanding/handlers.go", 320, 374), ("mathunderstanding/handlers.go", 258, 278), ("mathunderstanding/handlers.go", 674, 680), ("mathunderstanding/types.go", 315, 324)]),
        (8, "false_positive_context", "none",
         "SHA256 输入是账号/来源/配对限流桶的键，而非密码。LoginAttempt 只有租户、标识、IP 和服务端解析的 AccountID；Redis 的哈希只是稳定索引。不能把这个用法视作低熵密码存储，也不能把无密钥 SHA256 当作不可猜测的匿名化。",
         "保持限流桶和密码存储职责分开；若未来需要对低熵标识作保密匿名化，再使用独立有密钥方案。",
         [("auth/login_limiter_redis.go", 159, 162), ("auth/login_limiter.go", 17, 23), ("auth/login_limiter.go", 130, 142), ("auth/login_limiter.go", 168, 185)]),
        (9, "false_positive_context", "none",
         "HashToken 存储高熵随机令牌的摘要：NewToken 使用 crypto/rand 生成 32 字节，MFA 恢复码另为 16 字节随机数。激活/恢复流程明确将 input.Token 与 input.Password 分开。密码由 HashPassword 的带随机盐 Argon2id 处理，不能将 token 的快速摘要归为弱密码哈希；本条不评估全部认证流程。",
         "继续要求令牌具有充足随机熵，不让 HashToken 处理用户密码或未来的短码。",
         [("auth/token.go", 1, 23), ("auth/password.go", 31, 46), ("auth/activation.go", 101, 118), ("auth/recovery.go", 101, 119), ("auth/mfa.go", 145, 167)]),
        (10, "false_positive_context", "none",
         "ListPilotGates 在 make 前把 limit<=0 或 limit>500 改为100；有效分配容量最多500。本告警忽略了同函数中的上限归一化。",
         "保留归一化及租户过滤；本条不需要为扫描结果改动业务逻辑。",
         [("mathunderstanding/pilot_store.go", 47, 59)]),
        (11, "false_positive_context", "none",
         "ListExceptions 在 SQL 和 make 前把 Limit<=0 或 Limit>100 改为25，容量最多100，查询多取一行仍最多101。实际输入不是无界分配。",
         "保留查询与分配使用同一归一化值的顺序。",
         [("processing/store_postgres.go", 178, 215)]),
        (12, "false_positive_context", "none",
         "MemoryStore.claim 先调用 normalizeClaimInput，Limit<=0 或>100 被改为1；归一化返回值覆盖 input，之后才分配容量。因此这条告警中的 input.Limit 已有上限。",
         "保留归一化在锁内任务选取和容量分配之前的边界。",
         [("workerruntime/store_memory.go", 103, 116), ("workerruntime/validation.go", 47, 64)]),
        (13, "false_positive_context", "none",
         "PostgresStore.claim 同样先覆盖 input 为 normalizeClaimInput 的返回值，Limit 处于1..100；SQL LIMIT 与 make 的容量都使用该值。跨租户模式没有绕开这一步。本条只核验分配边界，不证明全部跨租户权限正确。",
         "保留归一化在事务和查询之前的顺序。",
         [("workerruntime/store_postgres.go", 112, 118), ("workerruntime/store_postgres.go", 212, 237), ("workerruntime/validation.go", 47, 64)]),
        (14, "conditional_deployment_review", "medium",
         "官方 Critical 9.1 数据流在 client.Do 命中用户配置的模型地址，但生产构造器绑定自定义 Transport：只接受 HTTPS、禁重定向、无环境代理；一次解析后检查全部 DNS 地址，再以已验证数字 IP 拨号，拒绝回环、私网、未指定、链路本地和多播。没有证明这些常见内网 SSRF 路径可绕过。publicProviderIP 未明确拒绝全部特殊用途地址（例如共享地址段）；其部署路由及管理员可配置目标的实际权限仍需单独核对，所以保留条件性部署检查，不标为已确认 SSRF，也不把当前列表称为所有特殊地址都安全。",
         "核对部署中的特殊地址段和提供商配置权限；按部署政策补充禁止网段或提供商允许列表，并保留 DNS 全量检查、数字 IP 拨号和禁止重定向。",
         [("modelgovernance/managed_config_probe.go", 47, 55), ("modelgovernance/managed_config_probe.go", 305, 317), ("modelgovernance/managed_config_probe.go", 386, 404), ("modelgovernance/managed_config_probe.go", 638, 644), ("modelgovernance/managed_config_probe.go", 739, 791)]),
        (15, "false_positive_context", "none",
         "传入日志的是独立 slog 属性 request_id；进入 preparedQuery 前 validateRequest 会 TrimSpace 并用 uuid.Parse 拒绝无效请求标识。攻击者的任意换行文字不能沿本告警路径进入此日志字段。不能从此推断所有其他日志字段也已审查。",
         "继续在 prepare 前校验请求标识，保留结构化属性输出。",
         [("schoolassistant/service.go", 44, 52), ("schoolassistant/service.go", 152, 161), ("schoolassistant/auth.go", 55, 61), ("schoolassistant/auth.go", 85, 95)]),
    ]
    findings = []
    for index, verdict, severity, reason, recommendation, contexts in cases:
        result = run["results"][index]
        rule = rules[result["ruleId"]]
        props = rule.get("properties", {})
        location = result["locations"][0]["physicalLocation"]
        path = location["artifactLocation"]["uri"]
        evidences = [evidence(*context) for context in contexts]
        assert any(x["path"] == path for x in evidences)
        findings.append({
            "review_id": f"CQ-GO-{index + 1:03d}", "sarif_result_index_zero_based": index,
            "rule_id": result["ruleId"], "path": path, "line": location["region"]["startLine"],
            "raw_level": result.get("level"), "raw_rule_default_level": rule.get("defaultConfiguration", {}).get("level"),
            "raw_problem_severity": props.get("problem.severity"),
            "effective_problem_level": result.get("level") or rule.get("defaultConfiguration", {}).get("level") or props.get("problem.severity"),
            "effective_level_derivation": "result.level, then rule.defaultConfiguration.level, then properties.problem.severity; original fields retained",
            "raw_security_severity": props.get("security-severity"), "raw_message": result["message"]["text"],
            "security_finding": "security" in props.get("tags", []), "manual_review_status": "reviewed_alert_context",
            "human_verdict": verdict, "human_severity": severity, "reason": reason, "notes": reason,
            "recommendation": recommendation, "evidence": evidences, "snapshot_sha256": hashes[path],
            "current_workspace_used_for_verdict": False,
        })
    part = {
        "reviewed_at": datetime.now(timezone(timedelta(hours=8))).isoformat(),
        "engine": "CodeQL CLI 2.27.2, Go security-and-quality 59 queries", "source_root": str(SOURCE),
        "sarif_sha256": hashlib.sha256((REPORT / "go.sarif").read_bytes()).hexdigest(),
        "manifest_sha256": hashlib.sha256(MANIFEST.read_bytes()).hexdigest(),
        "raw_go_results_total": len(run["results"]), "assigned_results_reviewed": len(findings),
        "assigned_sarif_result_indices_zero_based": [f["sarif_result_index_zero_based"] for f in findings],
        "human_verdict_counts": dict(Counter(f["human_verdict"] for f in findings)),
        "confirmed_business_runtime_defects_in_reviewed_results": 0,
        "scope": "指定9条安全告警上下文及其列明的保护调用；不是逐文件全仓人工审读。",
        "tests_executed_in_this_review": False, "business_sources_modified": False, "findings": findings,
    }
    (REPORT / "go-review-part-security.json").write_text(json.dumps(part, ensure_ascii=False, indent=2), encoding="utf-8")
    other = json.loads((REPORT / "go-review-part-quality.json").read_text(encoding="utf-8"))
    assert other["sarif_sha256"] == part["sarif_sha256"]
    assert other["manifest_sha256"] == part["manifest_sha256"]
    merged = sorted(other["findings"] + findings, key=lambda f: f["sarif_result_index_zero_based"])
    assert [f["sarif_result_index_zero_based"] for f in merged] == list(range(len(run["results"])))
    final = dict(part)
    final.update({"reviewed_alerts": len(merged), "finding_reviews_pending": 0,
                  "human_verdict_counts": dict(Counter(f["human_verdict"] for f in merged)), "findings": merged,
                  "scope": "全部23条Go候选的告警上下文已分别核验；1条特殊地址段部署前提仍待确认，1条资源预算建议保留。不是全仓逐文件人工审读。"})
    for key in ("assigned_results_reviewed", "assigned_sarif_result_indices_zero_based"):
        final.pop(key)
    (REPORT / "go-finding-review.json").write_text(json.dumps(final, ensure_ascii=False, indent=2), encoding="utf-8")
    rows = ["# CodeQL Go 23 条候选核验", "", f"核验时间：{part['reviewed_at']}。实际扫描执行和原始等级见 [CodeQL 主报告](README.md)。", "",
            "核验固定扫描快照，列明源码证据均与逐文件 SHA256 清单一致；没有修改业务源码或运行生产接口。自动候选保留官方安全分数，人工结论单列。", "",
            "全部23条候选已分配并检查告警上下文：14条上下文误报、1条有意测试行为、6条低影响维护项、1条资源预算加固、1条条件性部署检查。没有确认可利用的 Go 生产漏洞；部署前提待确认不等于风险已经排除。这也不代表逐文件人工审读或跨学校数据隔离已经验证完成。", "",
            "数学验证容量告警不具备已证明的整数溢出路径，但已有记录缺独立数量上限；模型探测具备 HTTPS、DNS 地址检查及禁重定向保护，特殊用途地址和部署路由仍应核对。", "",
            f"原始 SARIF SHA256：`{part['sarif_sha256']}`。结构化结论和证据见 [go-finding-review.json](go-finding-review.json)。", "",
            "| 编号 | 规则 | 固定快照位置 | 官方安全分数 | 人工结论 |", "| --- | --- | --- | --- | --- |"]
    for f in merged:
        rows.append(f"| {f['review_id']} | `{f['rule_id']}` | `{f['path']}:{f['line']}` | {f['raw_security_severity'] or '非安全候选'} | {f['human_verdict']} / {f['human_severity']} |")
    for f in merged:
        rows += ["", f"## {f['review_id']}：{f['rule_id']}", "", f"固定快照位置：`{f['path']}:{f['line']}`。", "", f["reason"], "", "建议：" + f["recommendation"], "", "已审读证据范围："]
        for e in f["evidence"]:
            rows.append(f"- `{e['path']}:{e['start_line']}–{e['end_line']}`，SHA256 `{e['snapshot_sha256']}`。")
    (REPORT / "go-finding-review.md").write_text("\n".join(rows) + "\n", encoding="utf-8")
    print(json.dumps({"reviewed": len(merged), "verdicts": final["human_verdict_counts"]}, ensure_ascii=False))


if __name__ == "__main__":
    main()
