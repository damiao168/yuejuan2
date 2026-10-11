"""汇总逐项修复证据和复扫；哈希清单不自动升级全文件审读状态。"""

import hashlib
import json
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[3]


def read(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None


def main():
    original = read(ROOT.parent / "findings-triage.json")
    evidence = ["infra-results.json", "model-results.json", "worker-results.json", "root-results.json"]
    decisions = {}
    for name in evidence:
        for group in read(ROOT / name)["findings"]:
            for identifier in group.get("ids", [group.get("id")]):
                assert identifier not in decisions, f"duplicate decision: {identifier}"
                status = "already_fixed" if group["status"] == "already_fixed_verified" else group["status"]
                decisions[identifier] = {"status": status, "change": group["change"], "evidence": name}
    rows = []
    for finding in original:
        row = {key: finding[key] for key in ("id", "path", "line", "rule", "human_status")}
        if finding["id"] in decisions:
            row.update(decisions[finding["id"]])
        else:
            assert finding["human_status"] == "false_positive", f"unhandled actionable finding: {finding['id']}"
            row.update(status="existing_control", change=finding["human_notes"], evidence="../findings-triage.json")
        row["baseline_sha256"] = digest(ROOT / "baseline/files" / finding["path"])
        row["current_sha256"] = digest(REPO / finding["path"])
        row["bytes_changed_since_task_start"] = row["baseline_sha256"] != row["current_sha256"]
        rows.append(row)
    assert len(rows) == 105 and len(decisions) == 37
    scan = read(ROOT / "rescan/scan-summary.json")
    scan_meta = read(ROOT / "rescan/scan-metadata.json")
    scoped_meta = read(ROOT / "rescan-report-scope/scan-metadata.json")
    supplemental = read(ROOT / "supplemental-xml-results.json")
    supplemental_scan = read(ROOT / "rescan-supplemental/scan-metadata.json")
    external_meta = read(ROOT / "rescan-latest-external/scan-metadata.json")
    external_inventory = read(ROOT / "rescan-latest-external/source-inventory.json")
    inventory = read(ROOT / "rescan/source-inventory.json")
    scanned = {entry["path"]: entry["sha256"] for entry in inventory["source_files"]}
    current_scanned = {entry["path"]: entry["sha256"] for entry in external_inventory["files"]}
    for row in rows:
        row["matches_rescan_snapshot"] = row["current_sha256"] == scanned.get(row["path"])
        row["matches_latest_verified_scan"] = row["current_sha256"] == current_scanned.get(row["path"], scanned.get(row["path"]))
        row["latest_scan_evidence"] = "rescan-latest-external/scan-metadata.json" if row["path"] in current_scanned else "rescan-report-scope/scan-metadata.json"
    counts = dict(Counter(row["status"] for row in rows))
    assert counts == {"fixed": 36, "existing_control": 68, "already_fixed": 1}, counts
    assert all(row["matches_latest_verified_scan"] for row in rows), "报告命中文件在最后扫描后有变化，需重新核对"
    assert scoped_meta["scope_paths_scanned_verified"] and scoped_meta["exit_code"] == 0
    assert supplemental_scan["scope_paths_scanned_verified"] and supplemental_scan["exit_code"] == 0
    assert external_meta["scope_paths_scanned_verified"] and external_meta["exit_code"] == 0
    supplemental_inventory = read(ROOT / "rescan-supplemental/source-inventory.json")
    assert all(digest(REPO / row["path"]) == row["sha256"] for row in supplemental_inventory["files"])
    result = {"date": "2026-10-11", "timezone": "Asia/Shanghai",
              "generated_utc": datetime.now(timezone.utc).isoformat(), "counts": counts,
              "original_findings": len(original), "pending_actionable_findings": 0,
              "scope": "原报告全部105条命中；修复37条可处理项中的36条，复核SG-090既有修复，68条误报/已有控制保留解释",
              "rescan": scan, "rescan_exit_code": scan_meta["exit_code"],
              "report_scope_rescan": scoped_meta, "supplemental_xml": supplemental,
              "supplemental_rescan": supplemental_scan, "latest_external_rescan": external_meta, "findings": rows}
    (ROOT / "findings-remediation.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    byte_records = [{"path": path, "baseline_sha256": digest(ROOT / "baseline/files" / path),
                     "current_sha256": digest(REPO / path), "rescan_sha256": scanned.get(path),
                     "latest_scan_sha256": current_scanned.get(path, scanned.get(path)),
                     "full_file_review": "pending"} for path in sorted({row["path"] for row in rows})]
    (ROOT / "file-byte-inventory.json").write_text(json.dumps({
        "record_kind": "字节比对清单；修复增量评审另见各分工与独立评审证据，未重新全文审读不自动升级",
        "files": byte_records,
    }, ensure_ascii=False, indent=2), encoding="utf-8")
    labels = {"fixed": "本次修复", "already_fixed": "既有修复已核验", "existing_control": "误报/已有控制"}
    report = [
        "# Semgrep 报告修复与验证结果", "", "日期：2026-10-11（Asia/Shanghai）。", "",
        "已处置原报告全部 **105 条命中**：**36 条本次修复、1 条既有修复复核、68 条误报/已有控制保留核验说明**。原报告可处理项均已完成代码处置；运行验收限制另列。", "",
        "## 修复内容", "",
        "- SG-013～015、092～094、097～102、104：13 个凭据请求入口拒绝自动跳转；同源也拒绝，失败时关闭原响应。保留初始目标、TLS、超时、响应限额、错误映射及任务租约。", 
        "- SG-001～011：11 个 Dependabot 更新项设置普通版本 7 天 cooldown。安全更新不受该缓冲期影响，官方依据见 [GitHub 文档](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference#cooldown)。",
        "- SG-034～037：两处 XML 读取入口使用 defusedxml，拒绝 DTD、实体及外部引用；固定工具依赖与维护说明已同步。",
        "- SG-044～045、047～048、050～051：三份 Nginx 使用固定上游 Host，并清除传入 Forwarded/X-Forwarded-Host。",
        "- SG-091：E2E 客户端最终镜像使用 UID/GID 10001 和可写 HOME；SG-090 的既有非 root 修复保留。",
        "- SG-105：E2E 客户端拒绝跳转，JSON 响应设 4 MiB 读取上限。", "",
        "完整复扫额外发现 build_preview.py 和 verify_typography.py 的3条同类XML命中；两个解析入口同步迁移到 defusedxml 并拒绝DTD，补充测试和维护说明。这些新增处置单独记录，不混入原105条统计。", "",
        "## 验证", "",
        "模型服务 173 项测试通过；Worker 首轮 OCR 358 项和 68 subtests、主观题22项、图像质量84项通过；页面处理顺序复跑163项通过。响应关闭修正后，根代理的网络/TLS/租约/错误/XML回归237项通过，其中包括20个资源释放用例。XML单独12项、架构工具Node测试3项通过。相关Go 7个包及E2E客户端包通过，定向Ruff和差异空白检查通过。", "",
        "三份配置用官方 Windows Nginx 1.29.0 真实运行，48 个回环请求通过；包括恶意 Host、伪造转发头、API/前端/健康、安全响应头及前端重定向兼容性。详见 [运行证据](nginx-authority-runtime.json)。", "",
        f"全仓复扫使用官方 Semgrep {scan['version']} 的 Windows beta 原生版、同一份735条规则配置（实际运行434条）、OSS和单job。补充XML修复前快照包含 {len(inventory['source_files'])} 个文本文件，引擎实际扫描 {scan['scanned_files']} 个目标，CLI记录586个忽略路径。退出码 {scan_meta['exit_code']}，快照内 **{scan['findings']} 条规则命中**、**{scan['errors']} 条PartialParsing诊断**。与原Docker扫描覆盖不同，不能把105→27全算成修复，也不能用退出码或命中消失证明生产安全。",
        "",
        "随后显式扫描报告范围及新增测试共46个文件（421条实际规则）：全部进入paths.scanned，原37个命中文件全覆盖，65条命中、3个PartialParsing。原rule/path减少42个，其中35个本次处置位置、1个SG-090既有修复、6个已有Nginx安全头匹配差异；另1个本次处置SG-034保留标准库生成import残余误报。新增两条subprocess命中是可信本地测试工具调用，已核对固定参数和输入边界。", "",
        f"补充XML修复后再显式扫描 {supplemental_scan['scope_count']} 个当前文件：{supplemental_scan['finding_count']} 条命中、{supplemental_scan['error_count']} 条诊断，全部目标覆盖；当前字节与该次扫描记录一致。新增19项测试通过，合并原XML测试共31项；临时正常输出与补充基线一致。证据见 [补充XML结果](supplemental-xml-results.json)。",
        "",
        f"收尾期间两份原误报测试文件又发生外部修改，另存当前固定副本并增量审核、显式扫描：{external_meta['finding_count']}条命中、{external_meta['error_count']}条诊断，目标全部覆盖，误报依据复核见 [最后增量核验](rescan-latest-external/review-results.json)。原105条全部当前文件字节与各自最后核验扫描一致；此前快照和原始扫描保持独立保留。", "",
        "复扫详细判定见 [复扫核验](rescan-triage.md)；[原始结果](rescan/semgrep-raw.json)、[元数据](rescan/scan-metadata.json)、[范围清单](rescan/source-inventory.json)另存。", "",
        "原Docker引擎与WSL调用超时，因此改用隔离虚拟环境安装的同版本官方Windows wheel。文件SHA-256已对照官方PyPI元数据，见 [工具来源](tools/tool-provenance.json)。配置本地规则、关闭metrics/version-check、空平台令牌，不调用ci/login/upload；Windows运行没有容器级禁网隔离。", "",
        "## 保留与边界", "",
        "首次 REPORT.md、原始扫描JSON、规则、历史迁移、生成SDK和带哈希提示词均保留；本次没有提交或推送。命中文件修复前原字节及既有测试备份在 baseline/files，原工作区状态在 baseline/workspace-status.txt；workspace-after-xml.patch 是较晚捕获的辅助差异，不能冒充开工前全仓补丁。",
        "",
        "修复期间另有8个命中文件发生外部变动（测试注释/格式、测试函数拆分、只读模型工具辅助函数提取等），未回退或覆盖；相关误报依据在当前命中函数仍成立。字节变化逐文件记录，不能据此宣称全文审读完成。", "",
        "本次未做Linux Docker镜像构建/最终UID运行验证、完整Compose/浏览器E2E、真实供应商或生产部署验证。两个E2E Dockerfile的部分解析涉及最终指令，告警消失仅作辅助记录；USER/目录权限以静态审核为依据。Nginx仍接收未知入口域名；上游authority已收敛，公网allowlist/TLS由实际部署核验。过期租约用例采用合成服务端403，不替代真实网关到期集成测试。依赖跳转的部署地址需改成最终授权地址。", "",
        "自动审批策略拒绝测试运行时目录清理，便携文件保留在test-runtime下，测试Nginx进程已经退出。", "",
        "## 逐项处置", "",
        "位置沿用首次扫描行号；当前代码位置可能变化。误报依据来自原逐项核验并结合当前增量、字节和相关回归复核；不将负向TLS测试、参数化SQL、请求Cookie fixture或非密码学重试抖动改写成漏洞修复。", "",
        "| ID | 原扫描位置 | 处置 | 实现/依据 |", "| --- | --- | --- | --- |",
    ]
    for row in rows:
        note = row["change"].replace("|", "\\|").replace("\n", " ")
        report.append(f"| {row['id']} | `{row['path']}:{row['line']}` | {labels[row['status']]} | {note} |")
    report.extend(["", "[机器可读逐项结果](findings-remediation.json) · [增量独立评审](review-results.json) · [文件字节比对](file-byte-inventory.json)", ""])
    (ROOT / "REPORT.md").write_text("\n".join(report), encoding="utf-8")
    print(json.dumps(counts, ensure_ascii=False))


if __name__ == "__main__":
    main()
