"""逐项归因复扫命中；忽略和解析差异不能作为修复或安全证明。"""

import json
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[3]


def read(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def main():
    raw = read(ROOT / "rescan/semgrep-raw.json")
    inventory = read(ROOT / "rescan/source-inventory.json")
    snapshot = Path(inventory["snapshot"])
    original = read(ROOT.parent / "findings-triage.json")
    rules = read(ROOT / "rescan/merged-official-rules.json")["rules"]
    # Semgrep 会将配置路径拼到 ID 前面；按完整官方 ID 匹配，不能只比末段短名。
    identifiers = sorted((rule["id"] for rule in rules), key=len, reverse=True)

    def canonical(identifier):
        return next(rule for rule in identifiers if identifier == rule or identifier.endswith("." + rule))

    def relative(path):
        return Path(path).relative_to(snapshot).as_posix()

    previous = {}
    for row in original:
        previous.setdefault((row["path"], canonical(row["rule"])), []).append(row)
    supplemental = read(ROOT / "supplemental-xml-results.json")
    rows = []
    for index, hit in enumerate(raw["results"], 1):
        path, rule = relative(hit["path"]), canonical(hit["check_id"])
        old = previous.get((path, rule), [])
        row = {"id": f"RS-{index:03d}", "path": path, "line": hit["start"]["line"],
               "rule": rule, "original_ids": [entry["id"] for entry in old]}
        if path == "docs/architecture/tools/build_diagrams.py":
            row.update(status="fixed_boundary_residual", note="标准库 ElementTree 只生成节点；磁盘回读已改用 defusedxml，显式拒绝 DTD。import 规则仍命中生成依赖，保留此残余误报，不删除生成代码。")
        elif path in ("docs/architecture/tools/build_preview.py", "docs/architecture/tools/verify_typography.py"):
            row.update(status="supplemental_fixed", note="这次全仓快照发现的同类解析入口；复扫后补充迁移到 defusedxml 并拒绝 DTD，正常及恶意 XML 通过临时文件回归。此条描述的是修复前快照，当前字节另见补充复扫。")
        elif path == "lab/scripts/run-graduation-audit.js":
            row.update(status="trusted_local_tool", note="本地审计脚本只调用代码内固定 npm/node 命令和固定参数；Windows cmd 分支同样没有请求数据或可变业务字段拼接，ComSpec 来自运行者环境。拥有本地执行权限是工具前提，未见服务端命令注入入口。未运行会覆盖实验账本的审计脚本。")
        elif path == "scripts/check-lab-integration.mjs":
            row.update(status="trusted_local_tool", note="本地门禁只运行固定 npm test/eval/gate 和固定 go test 包；命令参数不由API或报告字段提供，cwd 由仓库路径确定。Windows cmd 分支接受本地工具环境，未见外部请求到命令执行的路径。未运行会覆盖实验报告的完整门禁。")
        else:
            assert old and all(entry["human_status"] == "false_positive" for entry in old), (path, rule)
            row.update(status="existing_control", note=" / ".join(dict.fromkeys(entry["human_notes"] for entry in old)))
        rows.append(row)
    diagnostics = []
    for error in raw["errors"]:
        kind = error["type"][0] if isinstance(error["type"], list) else error["type"]
        diagnostics.append({"type": kind, "path": relative(error["path"]),
                            "lines": [span["start"]["line"] for span in error.get("spans", [])]})
    scanned = {relative(path) for path in raw["paths"]["scanned"]}
    missing = sorted({row["path"] for row in original} - scanned)
    result = {"snapshot": str(snapshot), "findings": rows, "counts": dict(Counter(row["status"] for row in rows)),
              "snapshot_source_files": len(inventory["source_files"]), "engine_scanned_files": len(scanned),
              "semgrepignore_skipped_from_cli": 586, "original_hit_files_not_scanned_in_full": missing,
              "diagnostics": diagnostics, "diagnostic_counts": dict(Counter(row["type"] for row in diagnostics)),
              "supplemental_evidence": "supplemental-xml-results.json",
              "supplemental_scanner_findings_count": supplemental["scanner_findings_count"],
              "supplemental_entry_count": len(supplemental["findings"])}
    (ROOT / "rescan-triage.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    lines = ["# 复扫命中与覆盖核验", "",
             "完整扫描在补充 XML 修复前快照上完成：3236 个文本源文件，2605 个 paths.scanned，27 条命中、18 个 PartialParsing 诊断、退出码0。", "",
             "CLI 记录586个路径被 .semgrepignore 模式忽略；其余45个源文件没有进入引擎 paths.scanned，不能逐文件推定过滤原因。与首次 Linux/Docker 的3197目标覆盖不等价，105→27不能全部归因于代码修复。", "",
             "原报告37个命中文件中14个未被完整复扫实际扫描；随后显式传入46个文件，paths.scanned逐个核对全覆盖，原37个文件全部包括在内。定向结果65条命中、3个PartialParsing、退出码0。", "",
             "定向计数变化：42个原rule/path命中减少=35个本次处置位置+SG-090既有修复1个+原生产Nginx重复安全头6个匹配差异。本次36条处置中的SG-034仍命中标准库生成import，但输入解析已加固，因此保留误报说明。新增2个subprocess命中在可信本地测试工具，详见[定向核验](rescan-report-scope/triage.md)。", "",
             "全仓新增3条XML命中涉及两个工具，已补充安全解析和测试；这些不计入原报告36条。补充代码与该次全仓快照字节不同，见[补充证据](supplemental-xml-results.json)及[补充扫描](rescan-supplemental/scan-metadata.json)。另外2条child_process为固定本地审计/门禁命令，逐项如下。", "",
             "扫描器部分解析涵盖两个E2E Dockerfile的最终指令；USER配置必须结合静态审核，不能用告警消失代替Docker运行身份验收。其余诊断为TS测试语法、Dockerfile/Bash片段和工作流嵌入脚本解析差异。未为了扫描器兼容而改写业务或测试语法。", "",
             "## 全仓27条命中", "", "| ID | 快照位置 | 处置 | 依据 |", "| --- | --- | --- | --- |"]
    for row in rows:
        note = row["note"].replace("|", "\\|").replace("\n", " ")
        lines.append(f"| {row['id']} | `{row['path']}:{row['line']}` | {row['status']} | {note} |")
    lines.extend(["", "## 原报告未进入全仓引擎扫描的文件", ""])
    lines.extend(f"- `{path}`（已由46文件显式扫描补齐）" for path in missing)
    lines.extend(["", "## 部分解析诊断", "", "| 文件 | 起始行 | 类型 |", "| --- | --- | --- |"])
    for row in diagnostics:
        lines.append(f"| `{row['path']}` | {', '.join(map(str, row['lines']))} | {row['type']} |")
    lines.extend(["", "[机器结果](rescan-triage.json) · [全仓扫描元数据](rescan/scan-metadata.json) · [定向元数据](rescan-report-scope/scan-metadata.json)", ""])
    (ROOT / "rescan-triage.md").write_text("\n".join(lines), encoding="utf-8")
    print(json.dumps(result["counts"], ensure_ascii=False))


if __name__ == "__main__":
    main()
