import collections
import datetime
import hashlib
import json
import re
from pathlib import Path

REPORT = Path(__file__).parent
TZ = datetime.timezone(datetime.timedelta(hours=8))


def read_json(name):
    path = REPORT / name
    return json.loads(path.read_text(encoding="utf-8-sig")) if path.is_file() else None


if __name__ == "__main__":
    create = read_json("rust-create-execution.json")
    analyze = read_json("rust-analyze-execution.json")
    if analyze and analyze["command"][3] != create["command"][3]:
        # 独立新库查询尚未结束时，旧库的成功记录与 SARIF 不能套到本次诊断。
        analyze = None
    source = Path(next(arg.split("=", 1)[1] for arg in create["command"] if arg.startswith("--source-root=")))
    log_path = REPORT / "rust-create.log"
    text = log_path.read_text(encoding="utf-8", errors="replace")
    warnings = []
    for log_line, raw in enumerate(text.splitlines(), 1):
        match = re.search(r"WARN (.+?\.rs):(\d+):(\d+): (.*)", raw)
        if not match:
            continue
        path = Path(match[1])
        line, column = int(match[2]), int(match[3])
        message = match[4].strip("`")
        source_bytes = path.read_bytes()
        row = source_bytes.split(b"\n")[line - 1]
        try:
            relative = path.relative_to(source).as_posix()
        except ValueError:
            relative = str(path)
        # 保留平台原始告警，不以文件被归档或查询退出成功抹掉局部解析失败。
        warnings.append({"log_line": log_line, "path": relative, "line": line, "column": column, "message": message,
                         "source_sha256": hashlib.sha256(source_bytes).hexdigest(),
                         "reported_byte": row[column - 1] if column <= len(row) else None,
                         "is_crlf_line_end": row.endswith(b"\r") and column == len(row)})
    sarif = read_json("rust.sarif") if analyze and analyze["exit_code"] == 0 else None
    notifications = [item for run in sarif.get("runs", []) for inv in run.get("invocations", [])
                     for item in inv.get("toolExecutionNotifications", [])] if sarif else []
    macro_warnings = sum("macro expansion failed" in item["message"] or "expected expression" in item["message"] for item in warnings)
    query_diagnostics = read_json("rust-diagnostic-query-summary.json")
    # 官方查询诊断必须属于当前数据库与当前 SARIF；旧库解码文件不能证明新库宏已解析。
    if not sarif or not query_diagnostics or query_diagnostics.get("database") != create["command"][3] or query_diagnostics.get("sarif_sha256") != hashlib.sha256((REPORT / "rust.sarif").read_bytes()).hexdigest():
        query_diagnostics = None
    unresolved_macros = query_diagnostics["counts"]["UnresolvedMacroCalls"] if query_diagnostics else None
    macro_limitation = "本轮宏和表达式警告仍存在，须保留未展开语义限制。" if macro_warnings else "本轮原生日志未返回宏和表达式警告；仍需官方未展开宏诊断查询确认，不以 Cargo 编译成功代替平台结果。"
    if not macro_warnings and unresolved_macros == 0:
        macro_limitation = "本轮原生日志没有宏和表达式警告，官方 UnresolvedMacroCalls 查询返回 0；该结论只针对当前数据库，不能证明所有未被规则建模的业务语义完整。"
    elif unresolved_macros:
        macro_limitation = f"官方 UnresolvedMacroCalls 查询返回 {unresolved_macros} 个未解析宏调用，保留对应语义漏报限制。"
    result = {"collected_at": datetime.datetime.now(TZ).isoformat(timespec="seconds"),
              "database": create["command"][3], "source_root": str(source), "create_exit_code": create["exit_code"],
              "analyze_exit_code": analyze["exit_code"] if analyze else None,
              "create_log_sha256": hashlib.sha256(log_path.read_bytes()).hexdigest(),
              "warnings_count": len(warnings), "warnings_by_file": dict(collections.Counter(item["path"] for item in warnings)),
              "macro_expression_warning_count": macro_warnings,
              "warning_contexts": warnings,
              "official_diagnostic_query_counts": query_diagnostics["counts"] if query_diagnostics else None,
              "official_diagnostic_query_summary": "rust-diagnostic-query-summary.json" if query_diagnostics else None,
              "failed_trap_file_entries": sum("Failed to write trap file" in line for line in text.splitlines()),
              "sarif_tool_execution_notifications": notifications,
              "limitations": ["被归档的维护源码数量不等于全部宏、字符串与类型语义完整。",
                              "CRLF 字面量警告的位置须核验；Rust 官方输入规范会先归一化 CRLF，但不能据此保证本提取器已正确解码。",
                              macro_limitation]}
    (REPORT / "rust-extraction-diagnostics.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({key: result[key] for key in ("create_exit_code", "analyze_exit_code", "warnings_count", "warnings_by_file", "failed_trap_file_entries")}, ensure_ascii=False))
