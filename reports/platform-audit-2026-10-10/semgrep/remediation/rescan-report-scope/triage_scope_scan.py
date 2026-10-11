"""记录定向复扫 65 条命中的人工核验依据，不抑制规则或改写源码。"""

from collections import Counter
import hashlib
import json
from pathlib import Path


OUTPUT = Path(__file__).resolve().parent
AUDIT = OUTPUT.parents[1]
comparison = json.loads((OUTPUT / "comparison.json").read_text(encoding="utf-8"))
scope = json.loads((OUTPUT / "scope.json").read_text(encoding="utf-8"))
snapshot = Path(scope["snapshot"])
raw = json.loads((OUTPUT / "semgrep-raw.json").read_text(encoding="utf-8"))
original = json.loads((AUDIT / "findings-triage.json").read_text(encoding="utf-8"))


def canonical(rule):
    return rule.split(".rescan.", 1)[1] if ".rescan." in rule else rule.removeprefix("audit.")


def assess(rule, path):
    if "httpsconnection-detected" in rule:
        return "false_positive_existing_controls", "请求只接受 HTTPS，DNS 全部地址要求 global，socket 固定已验证地址但保留原主机 SNI。TLS client context 强制证书链和主机名校验；HTTP 状态非2xx报错，不自动跟随重定向。"
    if "dynamic-urllib" in rule:
        return "false_positive_test_scope", "测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。"
    if "dangerous-subprocess" in rule:
        if path.startswith("ai-services/"):
            return "trusted_local_test_tool", "OpenSSL来自本地测试环境变量或shutil.which；要求绝对路径并strict resolve。参数为固定req选项与随机临时证书文件，shell未启用。测试操作者需要选择可信本地可执行文件，未见生产API用户输入。"
        return "trusted_local_test_tool", "命中测试者显式--nginx本地路径的固定-v版本查询；路径resolve为绝对地址，subprocess参数为数组，shell未启用。测试操作者需要选择可信本地可执行文件，未见生产API用户输入。"
    if "header-redefinition" in rule:
        if path.startswith("apps/"):
            return "false_positive_existing_controls", "health location声明add_header会停止继承；此处同时重复server层全部安全响应头和always，没有丢掉安全策略。"
        return "false_positive_test_scope", "隔离E2E网关health仅设置Content-Type；server层没有需要继承的add_header。此测试配置不能当作生产安全响应头策略。"
    if "unknown-value-with-script-tag" in rule:
        return "false_positive_test_scope", "MFA测试故意把script字样放入mock recovery_codes，断言confirm变uncertain且phase关闭；不是把不可信值写入DOM的生产代码。"
    if "use-defused-xml" in rule:
        return "false_positive_generation_import_after_fix", "SG-034的标准库ET仅用于Element/SubElement/indent/write生成与序列化；生成文件再解析使用safe_xml.parse(forbid_dtd=True)。import命中没有区分生成与解析，解析边界已修复但保留标准库生成API。"
    if "exec-detected" in rule:
        return "false_positive_test_scope", "测试从仓库固定脚本读取AST，只选择固定白名单的三个FunctionDef并compile/exec；不是生产接口接受的用户源码。仓库源文件本身需可信。"
    if "cookie-missing" in rule:
        if path.endswith("auth/cookies.go"):
            return "false_positive_existing_controls", "实际Cookie字面量包含HttpOnly=true和Secure=h.cookieSecure。构造器接收配置，生产类环境默认Secure=true并拒绝false启动；本地HTTP测试允许配置false。"
        return "false_positive_test_scope", "httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。"
    if "string-formatted-query" in rule:
        if path.endswith("files/store_postgres.go"):
            return "false_positive_existing_controls", "拼接部分仅由内部状态列表长度生成$7以后数字占位符及固定assetColumns；租户、资产、revision、状态和detail均通过args绑定。"
        if path.endswith("score/store_postgres.go"):
            return "false_positive_existing_controls", "where由固定SQL片段与按args长度生成的数字占位符组成；tenant/exam/status/query均绑定参数，查询文本不会拼为SQL代码。"
        if path.endswith("e2e_business_command_test.go"):
            return "false_positive_test_scope", "函数/触发器名由固定前缀和去连字符UUID生成，commandID也是本地UUID；在临时E2E数据库中构造回滚故障，没有外部字符串输入。"
        if path.endswith("e2e_mfa_test.go"):
            return "false_positive_test_scope", "表名只由调用处固定四表白名单传入；隔离角色由本地UUID生成且pgx.Identifier.Sanitize引用，密码也由固定前缀和UUID生成。"
        if path.endswith("e2e_compose_bootstrap_test.go") or path.endswith("e2e_postgres_test.go"):
            return "false_positive_test_scope", "数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。"
        if path.endswith("e2e_question_bank_import_test.go"):
            return "false_positive_test_scope", "故障触发器名称为固定字符串，拼入的secondID来自本地question fixture的UUID；在临时E2E数据库测试局部失败/重试，不接收用户SQL片段。"
        return "false_positive_test_scope", "qbCounts仅遍历函数内固定五表白名单，外部请求不能提供表名；仅统计隔离E2E数据库状态。"
    if "missing-ssl-minversion" in rule:
        return "false_positive_test_scope", "这是故意设置MaxVersion=TLS1.1的httptest服务端，用来断言真实MinIO客户端在发送HTTP前拒绝旧协议；不是客户端生产TLS配置。"
    if "math-random-used" in rule:
        return "false_positive_existing_controls", "math/rand只生成幂等模型列表探测重试的短暂停顿jitter；令牌、密钥与认证随机数没有使用此调用。"
    if "avoid-pickle" in rule:
        return "false_positive_test_scope", "仅pickle.dumps可信测试内存结果，断言大于管道缓冲区以覆盖spawn回归；没有pickle.loads/load读取外部数据。"
    if "last-user-is-root" in rule:
        return "false_positive_with_partial_parsing", "OCR USER root用于依赖安装，真实文件末尾USER nobody后才运行CMD。此文件PartialParsing跨越最后USER，不能把扫描解析到的root解释为最终运行身份；Docker运行身份未验收。"
    if "hardcoded-password" in rule:
        return "false_positive_test_scope", "默认字符串仅在tests/test_main的_config返回给FakeClient/FakeRunner和monkeypatch；不是生产Worker密码配置。报告不复述测试值。"
    raise RuntimeError("未人工核验的规则: " + rule)


findings = []
for index, finding in enumerate(raw["results"], 1):
    rule = canonical(finding["check_id"])
    path = finding["path"].replace("\\", "/")
    source = (snapshot / path).read_bytes()
    offset = finding["start"]["offset"]
    actual_line = source[:offset].count(b"\n") + 1
    status, reason = assess(rule, path)
    candidates = [old["id"] for old in original if old["path"] == path and canonical(old["rule"]) == rule]
    findings.append({"id": f"RS-{index:03d}", "rule": rule, "path": path,
                     "reported_line": finding["start"]["line"], "source_line_from_byte_offset": actual_line,
                     "source_match_sha256": hashlib.sha256(source[offset:finding["end"]["offset"]]).hexdigest(),
                     "severity": finding["extra"]["severity"], "status": status, "reason": reason,
                     "original_rule_path_ids": candidates, "new_rule_path": not bool(candidates),
                     "association_limit": "原编号按rule/path关联，不声称旧行号与新行号一对一映射",
                     "finding_review_complete": True, "whole_file_review_status": "pending"})
disappeared = comparison["disappeared_rule_path_counts"]
for group in disappeared:
    if group["path"] == "infra/docker-compose/nginx/nginx.conf" and "header-redefinition" in group["rule"]:
        group["cause"] = "context_matching_difference_existing_headers_unchanged"
    elif "SG-090" in group["original_ids"]:
        group["cause"] = "already_fixed_before_this_remediation"
    else:
        group["cause"] = "remediated_in_this_task"
breakdown = Counter()
for group in disappeared:
    breakdown[group["cause"]] += group["count_difference"]
assert len(findings) == 65
assert breakdown == {"remediated_in_this_task": 35, "already_fixed_before_this_remediation": 1,
                      "context_matching_difference_existing_headers_unchanged": 6}
report = {"scan_kind": "report_scope_fixed_snapshot", "all_scoped_paths_scanned": True,
          "scoped_paths": 46, "original_finding_paths_covered": 37, "findings_reviewed": len(findings),
          "pending_finding_reviews": 0, "whole_file_review_claimed": False, "findings": findings,
          "status_counts": dict(Counter(finding["status"] for finding in findings)),
          "disappeared_rule_path_count": 42, "disappeared_breakdown": dict(breakdown),
          "this_task_dispositions": {"total": 36, "no_longer_matching": 35,
                                     "fixed_boundary_but_residual_generation_import_false_positive": ["SG-034"]},
          "disappeared_groups": disappeared, "diagnostics": comparison["diagnostics"],
          "limits": comparison["interpretation_limits"]}
(OUTPUT / "triage.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
lines = ["# 报告范围定向复扫核验", "",
         "2026-10-11，Semgrep 1.179.0 Windows native beta / OSS；735条配置，421条实际运行，21.875秒。",
         "46个显式固定副本文件全部列入 paths.scanned，包括原105条命中的全部37个文件；没有按目录默认忽略tests来代替报告文件验收。这是报告范围定向复扫，不是全仓复扫。", "",
         "真实结果：65条命中、3条PartialParsing。65条均结合命中原字节offset、相邻代码及调用来源重新核验，未抑制规则，未为消除误报修改业务代码；27个命中文件不因此被声称已完成整文件审读。", "",
         "按规范化rule/path计数，原105条中63条类计数保留、42条减少、2条新增。42条减少 = 35个本次处理位置不再命中 + SG-090此前已修复1条 + Nginx既有安全头上下文匹配差异6条。本次36个处置位置 = 35个不再命中 + SG-034解析边界修复后仍残留的标准库XML生成import误报1条。不能把42的扫描减幅写成42处漏洞修复。", "",
         "SG-034残余import只使用标准库ET生成、排版和序列化；解析使用defusedxml safe_xml.parse并禁止DTD。新增2条dangerous-subprocess命中分别是TLS测试固定OpenSSL证书生成和Nginx测试固定-v查询，可执行文件只由本地测试操作者选择、参数用数组且不开shell，未见生产API用户可控命令。", "",
         "三条PartialParsing涉及两个E2E Dockerfile和OCR Dockerfile。SG-091的失败解析区域包含最终USER，因此原USER类告警消失不能作为运行身份保证；静态代码明确USER 10001:10001，但Docker引擎无响应，未声称容器运行身份通过。OCR仍命中安装阶段USER root；完整文件末尾为USER nobody。", "",
         "Windows结果部分Go命中的reported line与原字节offset推算行号不同；下表同时保留二者，原始JSON没有改写。46个扫描副本SHA-256均与比对时工作区相同，见comparison.json。", "",
         "| 编号 | 文件 | Semgrep行 / 原字节行 | 核验结论 | 依据 |", "| --- | --- | --- | --- | --- |"]
labels = {"false_positive_existing_controls": "误报/已有控制", "false_positive_test_scope": "误报/测试fixture",
          "trusted_local_test_tool": "新增/可信本地测试工具", "false_positive_generation_import_after_fix": "修复后XML生成import残余误报",
          "false_positive_with_partial_parsing": "误报/须保留解析限制"}
for finding in findings:
    lines.append(f"| {finding['id']} | `{finding['path']}` | {finding['reported_line']} / {finding['source_line_from_byte_offset']} | {labels[finding['status']]} | {finding['reason']} |")
(OUTPUT / "triage.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
print(json.dumps({"reviewed": len(findings), "status_counts": report["status_counts"],
                  "disappeared_breakdown": report["disappeared_breakdown"]}, ensure_ascii=False))
