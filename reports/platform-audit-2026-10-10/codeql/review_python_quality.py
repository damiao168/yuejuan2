"""复现本次 Python 质量告警的人工核验汇总；只读固定快照，不启动应用。"""
from __future__ import annotations

import ast
import collections
import csv
import hashlib
import json
import math
import pathlib
import threading
import time
from collections.abc import Callable
from datetime import datetime, timezone
from types import SimpleNamespace
from typing import Any, Self

OUT = pathlib.Path(__file__).resolve().parent
SOURCE = pathlib.Path(r"D:\tools\audit-codeql-20261010\source-20261011")
SARIF = OUT / "python.sarif"
MANIFEST = OUT.parent / "source-manifest-resume-2026-10-11.csv"


def sha(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def evidence(path: str, start: int, end: int | None = None) -> dict:
    return {"path": path, "start_line": start, "end_line": end or start,
            "snapshot_sha256": sha(SOURCE / path)}


def callback_check() -> dict:
    """仅执行选取的类定义和构造器；假 API 不联网，未调用 __enter__ 或启动线程。"""
    base_path = "packages/worker-runtime-python/edugrade_worker_runtime/lease.py"
    base_tree = ast.parse((SOURCE / base_path).read_text(encoding="utf-8-sig"))
    chosen = [node for node in base_tree.body
              if isinstance(node, (ast.FunctionDef, ast.ClassDef))
              and node.name in {"validate_lease_timing", "LeaseHeartbeat"}]
    future = ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0)
    namespace = {"math": math, "threading": threading, "time": time,
                 "Callable": Callable, "Self": Self}
    module = ast.fix_missing_locations(ast.Module(body=[future, *chosen], type_ignores=[]))
    exec(compile(module, str(SOURCE / base_path), "exec"), namespace)
    base = namespace["LeaseHeartbeat"]
    scenarios = [
        ("services/subjective-grading-worker/subjective_grading_worker/runner.py", "_Heartbeat", "subjective"),
        ("services/ocr-worker/ocr_worker/runner.py", "_LeaseHeartbeat", "ocr"),
        ("services/page-processing-worker/page_processing/runner.py", "_LeaseHeartbeat", "page"),
    ]
    checks = []
    for path, name, label in scenarios:
        events: list[str] = []

        class FakeAPI:
            def heartbeat(self, *args, **kwargs):
                events.append("heartbeat")

            def heartbeat_task(self, *args, **kwargs):
                events.append("heartbeat_task")

        tree = ast.parse((SOURCE / path).read_text(encoding="utf-8-sig"))
        selected = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == name)
        ns = {"LeaseHeartbeat": base, "Any": Any, "Settings": object,
              "Client": object, "threading": threading, "_Heartbeat": type("ProtocolStub", (), {})}
        mod = ast.fix_missing_locations(ast.Module(body=[future, selected], type_ignores=[]))
        exec(compile(mod, str(SOURCE / path), "exec"), ns)
        klass = ns[name]
        fake_api = FakeAPI()
        if label == "subjective":
            settings = SimpleNamespace(worker_id="audit-worker", heartbeat_interval=5,
                                       lease_seconds=60, heartbeat_timeout=2)
            obj = klass(fake_api, "audit-task", "audit-placeholder", settings)
        elif label == "ocr":
            obj = klass(api=fake_api, runtime_task_id="audit-task", lease_token="audit-placeholder",
                        worker_id="audit-worker", interval=5, lease_seconds=60, request_timeout=2)
        else:
            obj = klass(client=fake_api, task={}, worker_id="audit-worker", interval=5,
                        lease_seconds=60, request_timeout=2)
        bound = obj.__dict__["_send"]
        assert callable(bound) and bound.__self__ is obj and bound.__func__ is klass.__dict__["_send"]
        obj._send()
        assert len(events) == 1 and obj._thread is None
        checks.append({"path": path, "callback_remains_callable": True,
                       "bound_to_same_object_and_subclass_method": True,
                       "fake_api_calls": events, "thread_started": False})
    return {"method": "AST-selected actual class definitions with fake API/type stubs",
            "application_imported": False, "network_used": False, "threads_started": False,
            "limitations": "Checks construction and one callback dispatch only; not lease/concurrency integration tests.",
            "cases": checks}


def review(rule: str, path: str, line: int) -> tuple[str, str, str, str, list[dict]]:
    if rule == "py/attribute-shadows-method":
        ranges = {"services/subjective-grading-worker/subjective_grading_worker/runner.py": (56, 68),
                  "services/ocr-worker/ocr_worker/runner.py": (336, 385),
                  "services/page-processing-worker/page_processing/runner.py": (488, 521)}
        a, b = ranges[path]
        return ("intentional_behavior", "none", "构造器先取得 self._send 的子类绑定方法，再传给父类；父类把同一 callable 存入实例 _send。确有名称遮蔽，但调用仍绑定同一对象和子类实现，不是续租回调被非 callable 覆盖。隔离构造核验三个类均派发到假 API，未启动线程。",
                "可在后续维护中把父类存储字段改为 _send_callback，或补充回调注入说明，减少非直观名称遮蔽；本次无需按运行故障修复。",
                [evidence(path, a, b), evidence("packages/worker-runtime-python/edugrade_worker_runtime/lease.py", 37, 63), evidence("packages/worker-runtime-python/edugrade_worker_runtime/lease.py", 89, 97)])
    if rule == "py/empty-except":
        fallbacks = {169: (163, 171, "/proc/cpuinfo 不可用时回退 platform.processor/unknown"),
                     179: (174, 188, "cgroup v2 CPU 限额探测失败时继续尝试 v1"),
                     186: (174, 188, "cgroup v1 探测失败时返回 None 表示未知限额"),
                     200: (191, 209, "cgroup 内存限制不可用时继续读取 /proc/meminfo"),
                     207: (191, 209, "主机内存不可读时使用其他已收集候选，全部不可用才返回 0")}
        a, b, fallback = fallbacks[line]
        return ("low_maintenance", "low", fallback + "。这是可选硬件描述探测，不是吞掉成绩写入或权限校验异常；但 except 分支缺少解释，权限/格式错误和正常跨平台缺失无法直接区分，符合低影响可维护性告警。",
                "在 except 内用简短中文说明允许忽略的探测失败和后续回退；如需诊断仅记录不含敏感值的 debug 原因，避免把缺失探测强行当成已知容量。",
                [evidence(path, a, b)])
    if rule == "py/implicit-string-concatenation-in-list":
        scope = (56, 69) if line != 81 else (80, 82)
        return ("false_positive_context", "none", "相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。",
                "保留输出语义；可用括号或显式拼接表达段落边界以减少告警，不能直接在每行末尾加逗号改变文档段落。",
                [evidence(path, *scope)])
    if rule == "py/unnecessary-lambda":
        return ("low_maintenance", "low", "测试 monkeypatch 使用无参数 lambda: _config()，只转调本文件默认配置工厂；本测试范围内没有替换 _config 的后续步骤。告警为真实但低影响的测试写法冗余，不影响阅卷运行。",
                "可把这两处 lambda 换成 _config；带自定义参数的其他 lambda 不属于此结论。",
                [evidence(path, 47, 87)])
    if rule == "py/procedure-return-value-used":
        return ("intentional_behavior", "none", "snapshot() 写基线并打印摘要，没有返回值；其 None 仅传给 sys.exit，Python 对 sys.exit(None) 使用成功退出状态。--verify 分支则保留 verify() 的 0/1 返回值。没有把 None 当作快照数据使用。",
                "可让 snapshot() 显式 return 0 或分开两条 sys.exit 分支以提高清晰度；不应把此告警描述为基线校验失效。",
                [evidence(path, 22, 48)])
    if rule == "py/import-and-import-from":
        return ("intentional_behavior", "none", "Runner 类用于构造和类型注解，runner_module 模块对象用于 monkeypatch 其依赖函数。两种导入承担不同测试职责，不是重复执行模块或导错对象。",
                "可统一通过 runner_module.Runner 引用以减少风格告警；保留双导入也不会产生已证实的运行问题。",
                [evidence(path, 8, 22), evidence(path, 41, 55), evidence(path, 97, 100)])
    if rule == "py/unused-import":
        return ("low_maintenance", "low", "同一 import 语句中的 re 和 collections 在固定快照文件后续均无引用。SARIF 把两项 unused import 合并成一条结果；不要计作两条漏洞。工具只生成架构文档，影响为多余依赖和可读性。",
                "后续清理该语句里的 re、collections，保留实际使用的 json、hashlib；本次不修改生成工具。",
                [evidence(path, 1, 14)])
    if rule == "py/ineffectual-statement":
        protocol_ranges = {
            "services/ocr-worker/ocr_worker/engine.py": (20, 28),
            "services/ocr-worker/ocr_worker/math_layout.py": (14, 22),
            "ai-services/grading_agent/provider_adapter.py": (8, 22),
            "services/ocr-worker/ocr_worker/recognition_router.py": (72, 80),
            "services/page-processing-worker/page_processing/runner.py": (34, 44),
            "services/image-quality-worker/image_quality/runner.py": (14, 34),
        }
        return ("false_positive_context", "none", "命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。",
                "保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。",
                [evidence(path, *protocol_ranges[path])])
    if rule == "py/unused-global-variable":
        if path.endswith("math_layout.py"):
            if line == 244:
                refs = [evidence("services/ocr-worker/ocr_worker/paper_formula.py", 13, 23),
                        evidence("services/ocr-worker/ocr_worker/paper_formula.py", 188, 205),
                        evidence("services/ocr-worker/tests/test_paper_formula.py", 103, 116)]
                detail = "_adaptive_crop_formula 被 paper_formula.py 实际导入并调用，也由测试引用。"
                status = "false_positive_context"
            elif line == 246:
                refs = [evidence("services/ocr-worker/tests/test_paper_formula.py", 10, 10),
                        evidence("services/ocr-worker/tests/test_paper_formula.py", 51, 57)]
                detail = "_merge_formula_boxes 被 test_paper_formula.py 实际导入并调用。"
                status = "false_positive_context"
            else:
                refs = []
                detail = "固定快照搜索未找到此兼容别名的实际消费者；邻近注释明确将它作为旧接口兼容别名保留。仅凭本仓库没有使用者不能证明不存在旧调用方。"
                status = "intentional_behavior"
            return (status, "none", detail + "公开实现仍在使用，告警不是裁剪/合并算法失效。",
                    "有实际消费者的别名应保留或先迁移调用方；无本仓消费者的兼容别名在确认兼容期结束后才删除。",
                    [evidence(path, 242, 247), *refs])
        if path.endswith("paper_compact/schema.py"):
            references = {
                19: [("anchors.py", 615, 617), ("chunks.py", 138, 146)],
                20: [("anchors.py", 155, 170), ("chunks.py", 138, 146)],
                24: [("anchors.py", 159, 173), ("anchors.py", 237, 246)],
                27: [("anchors.py", 217, 220), ("anchors.py", 273, 279)],
                28: [("anchors.py", 303, 311), ("anchors.py", 393, 399)],
                29: [("anchors.py", 428, 436), ("anchors.py", 615, 617)],
                30: [("anchors.py", 615, 625)],
                33: [("anchors.py", 615, 625)],
                36: [("anchors.py", 615, 625)],
            }
            refs = [evidence("ai-services/grading_agent/paper_compact/" + f, a, b)
                    for f, a, b in references[line]]
            return ("false_positive_context", "none", "该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。",
                    "保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。",
                    [evidence(path, line, min(line + 2, 38)), *refs])
        if path.endswith("verify_database_catalog.py"):
            if line == 62:
                scope = (60, 69)
                detail = "columns 在紧随其后的 FK 候选列表推导和唯一性生成器中被读取，参与字段与基数验证。"
            else:
                scope = (90, 95)
                detail = "fk 在 any() 的生成器表达式中被读取（fk[2]/[3]/[4]/[6]），参与 SQLite 外键动作交叉核对。"
            return ("false_positive_context", "none", detail + "不能把推导式内部引用当成全局变量从未使用。",
                    "保留验证逻辑；如为可读性把脚本主体封装为 main()，需确认行为相同，不能直接删除这两项赋值。",
                    [evidence(path, *scope)])
    raise AssertionError((rule, path, line))


def main() -> None:
    sarif = json.loads(SARIF.read_text(encoding="utf-8-sig"))
    run = sarif["runs"][0]
    rules = {r["id"]: r for r in run["tool"]["driver"]["rules"]}
    with MANIFEST.open(encoding="utf-8-sig", newline="") as f:
        manifest = {row["path"]: row["sha256"] for row in csv.DictReader(f)}
    rows = []
    for i, raw in enumerate(run["results"], 1):
        if raw["ruleId"] == "py/polynomial-redos":
            continue
        loc = raw["locations"][0]["physicalLocation"]
        path = loc["artifactLocation"]["uri"]
        line = loc["region"]["startLine"]
        assert sha(SOURCE / path) == manifest[path], path
        status, severity, reason, recommendation, refs = review(raw["ruleId"], path, line)
        rule = rules[raw["ruleId"]]
        rows.append({"review_id": f"CQ-PY-{i:03d}", "sarif_result_index_zero_based": i - 1,
                     "rule_id": raw["ruleId"], "path": path, "line": line,
                     "end_line": loc["region"].get("endLine", line),
                     "raw_level": raw.get("level", rule["defaultConfiguration"]["level"]),
                     "raw_problem_severity": rule.get("properties", {}).get("problem.severity"),
                     "raw_message": raw["message"]["text"], "security_finding": False,
                     "manual_review_status": "reviewed_alert_context", "human_verdict": status,
                     "human_severity": severity, "reason": reason, "recommendation": recommendation,
                     "evidence": refs, "snapshot_sha256": sha(SOURCE / path),
                     "current_workspace_used_for_verdict": False})
    assert len(rows) == 54 and len({r["review_id"] for r in rows}) == 54
    counts = dict(collections.Counter(r["human_verdict"] for r in rows))
    rule_counts = dict(collections.Counter(r["rule_id"] for r in rows))
    checks = callback_check()
    report = {"reviewed_at": datetime.now(timezone.utc).isoformat(),
              "source_root": str(SOURCE), "sarif_sha256": sha(SARIF),
              "manifest_sha256": sha(MANIFEST), "codeql_version": run["tool"]["driver"]["semanticVersion"],
              "raw_python_results_total": len(run["results"]), "raw_quality_results_reviewed": len(rows),
              "excluded_security_rules": ["py/polynomial-redos"], "finding_reviews_pending": 0,
              "confirmed_runtime_defects_in_reviewed_quality_results": 0,
              "human_verdict_counts": counts, "quality_rule_counts": rule_counts,
              "snapshot_validation": "Every alert file SHA256 matches the fixed snapshot manifest.",
              "scope": "Each alert and listed support context reviewed; not a whole-file or whole-repository completion claim.",
              "callback_check": checks, "findings": rows}
    (OUT / "python-quality-review.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    labels = {"false_positive_context": "上下文误报", "intentional_behavior": "有意实现／兼容行为", "low_maintenance": "低影响维护项"}
    text = ["# CodeQL Python 质量告警人工核验", "",
            "本附件逐条核验 `python.sarif` 中的 54 条质量候选；另 1 条 `py/polynomial-redos` 安全候选由主 CodeQL 报告单独处理，未混入本附件统计。自动告警的 error/note 等级保留，人工判断另列。", "",
            f"固定快照：`{SOURCE}`，CodeQL {report['codeql_version']}。不以当前工作区的后续修复将快照告警判为误报；所有命中文件 SHA256 均与清单一致。", "",
            f"原始 SARIF SHA256：`{report['sarif_sha256']}`。快照源清单 SHA256：`{report['manifest_sha256']}`。", "",
            "## 核验统计", "", "| 分类 | 数量 | 含义 |", "| --- | ---: | --- |",
            f"| 上下文误报 | {counts.get('false_positive_context', 0)} | 接口占位、跨文件消费者、推导式引用或有意段落拼接 |",
            f"| 有意实现／兼容行为 | {counts.get('intentional_behavior', 0)} | 现有语义可解释；可选命名/兼容接口整理 |",
            f"| 低影响维护项 | {counts.get('low_maintenance', 0)} | 注释或写法应改进，不是已确认生产漏洞 |", "",
            "54 条告警上下文均已核验，无待核验条目；其中没有确认会导致运行错误的质量缺陷。这不代表全仓库人工审读完成，也不证明阅卷正确率、续租并发或权限边界已通过集成测试。", "",
            "| 规则 | 原始候选数 |", "| --- | ---: |"]
    text += [f"| `{rule}` | {n} |" for rule, n in rule_counts.items()]
    text += ["", "## 主要判断及边界", "",
             "5 条空 except 位于 OCR 可选硬件描述探测：Linux /proc 或 cgroup 文件缺失、限额格式不可用时允许回退。现有分支缺少允许忽略及回退边界说明，属于低影响维护问题。建议按仓库注释标准补充说明，必要时提供不含敏感值的 debug 诊断；无需把正常跨平台缺失升级为致命错误。", "",
             "3 条 error 级方法遮蔽均为 LeaseHeartbeat 回调注入：子类绑定方法先传入父类，随后父类将同一 callable 存在 `_send` 字段。用固定快照 AST 选取真实类定义和构造器、假 API 与类型桩，三个对象的 `_send` 都仍绑定到同一实例及子类方法，各成功触发一次假 API 调用；未导入应用、未启动线程、未联网。这仅验证构造和派发语义；不替代续租超时及并发测试。建议后续将存储字段命名为 `_send_callback` 或解释注入关系，减少维护困惑。", "",
             "22 条无效语句命中 typing.Protocol 接口方法中的 `...`；它们用于类型接口声明。9 条 schema 正则与 2 条数学兼容别名在其他快照文件有实际消费者；2 条数据库脚本变量被推导式／生成器读取。不能仅据定义文件内无引用而删除这些内容。", "",
             "剩余 2 个数学兼容别名在该快照没有搜索到消费者，但附近注释明确保留兼容接口：当前核验只接受这一有限兼容用途，未证明外部依赖存在，也未建议无条件保留永久死代码。", "",
             "4 条字符串拼接属于连续说明段落；1 条 snapshot() 返回值只是交给 sys.exit(None) 使用成功状态；测试同时导入模块和 Runner 分别用于 monkeypatch 与实例构造。真实但低影响的另外 3 条写法问题是 2 个冗余测试 lambda 和 1 条合并了 re/collections 两项的未使用 import 告警。", "",
             "## 逐条对应", "",
             "编号对应 SARIF 结果的一基序号；CQ-PY-001 是单独处理的安全候选。JSON 同时保存零基索引、原始等级/消息、完整证据文件哈希与审读行段。表中位置均指固定快照。", "",
             "| 编号 | 原始规则／等级 | 快照位置 | 人工判断 | 依据与建议 |",
             "| --- | --- | --- | --- | --- |"]
    for row in rows:
        reason = row["reason"].replace("|", "\\|")
        recommendation = row["recommendation"].replace("|", "\\|")
        text.append(f"| {row['review_id']} | `{row['rule_id']}` / {row['raw_level']} | `{row['path']}:{row['line']}` | {labels[row['human_verdict']]} | {reason}<br>建议：{recommendation} |")
    text += ["", "## 复现与交付范围", "",
             "`review_python_quality.py` 保存本次人工判断映射和隔离构造核验；执行它只读固定快照及原始 SARIF，并重生成本附件 JSON/Markdown，不修改业务源码。人工判断映射不会自动证明将来的源码版本；快照哈希变化时需要重新审读。", "",
             "本附件只负责 54 条 Python 质量候选，不修改主 CodeQL README、summary 或 alerts.csv。安全正则、JavaScript/Actions/Go/Rust 引擎结果以主报告为准。", ""]
    (OUT / "python-quality-review.md").write_text("\n".join(text), encoding="utf-8")
    assert "\ufffd" not in (OUT / "python-quality-review.md").read_text(encoding="utf-8")
    print(json.dumps({"reviewed": len(rows), "counts": counts, "callback_checks": len(checks['cases'])}))


if __name__ == "__main__":
    main()
