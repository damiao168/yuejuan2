# Python CodeQL 候选修复与闭环

原 python.sarif 共 55 条：修复 12 条，上下文误报 39 条，有意兼容行为 4 条，pending 0 条。所有编号保留原零基 resultIndex；原扫描及人工核验附件未改写。

页脚候选先检查去掉首尾空白后的 80 字符预算，再使用无歧义占有量词正则。超长候选不按单页强页脚规则排除，原有跨页重复页边规则仍适用，原始 OCR 证据不截断；120 种合法前缀、Unicode 空白、标点及总页数组合有回归验证。共享心跳回调改名后不覆盖三个 worker 子类的 _send 方法。

相关测试合计 **260 passed、53 subtests passed**；七个改动 Python 文件语法检查通过，五处硬件探测说明已重建原始基线 SHA256 并确认 AST 不变。六个既有文件仅在内存中逆转本次修改后均与保存的基线 SHA256 完全一致，git diff --check 通过，原有工作区修改得到保留。

没有重新运行 CodeQL，不声明新扫描候选归零。五条空 except 保留跨平台可选探测的允许回退行为，以准确中文说明完成维护项。此报告只覆盖候选及所需调用/测试上下文，不等同于全文件或全仓人工审读。

## 改动文件

- `ai-services/grading_agent/paper_compact/schema.py`
- `ai-services/grading_agent/paper_compact/anchors.py`
- `ai-services/tests/test_paper_footer_security.py`
- `packages/worker-runtime-python/edugrade_worker_runtime/lease.py`
- `services/ocr-worker/ocr_worker/deployment_profile.py`
- `services/page-processing-worker/tests/test_main.py`
- `docs/architecture/tools/build_database_specs.py`

## 验证

| 命令/验证范围 | 结果 |
| --- | --- |
| python -m pytest ai-services/tests/test_paper_footer_security.py ai-services/tests/test_paper_compact.py ai-services/tests/test_paper_compact_markers_batch34.py ai-services/tests/test_paper_parser.py ai-services/tests/test_paper_input.py -q --tb=short | 93 passed, 2 subtests passed |
| python -m pytest services/page-processing-worker/tests/test_main.py services/page-processing-worker/tests/test_runner_heartbeat.py services/page-processing-worker/tests/test_runner_state_boundaries.py -q --tb=short | 47 passed |
| python -m pytest services/ocr-worker/tests/test_runner.py services/ocr-worker/tests/test_api_batch51.py services/ocr-worker/tests/test_formula_calibration.py services/ocr-worker/tests/test_paper_formula.py -q --tb=short | 72 passed, 5 subtests passed |
| python -m pytest services/subjective-grading-worker/tests/test_worker.py services/subjective-grading-worker/tests/test_api_integration.py -q --tb=short | 7 passed |
| python -m pytest services/ocr-worker/tests/test_config.py services/ocr-worker/tests/test_startup_batch36.py -q --tb=short | 41 passed, 46 subtests passed |
| compile() all seven changed Python files; reconstruct deployment_profile.py baseline bytes without the five new comments and compare SHA256/AST | 7 files compile; deployment_profile baseline SHA256 reconstructed; AST unchanged |
| Current _PAGE_FOOTER and _is_page_furniture synthetic benchmark, 512 through 700000 internal whitespace characters | 700000 chars: regex about 0.00350 s; furniture about 0.02196 s; all failing suffixes rejected and retained |

性能实验见 [python-footer-benchmark.json](python-footer-benchmark.json)。700000 个内部空白加失败后缀，当前正则约 0.00350 秒、页边识别约 0.02196 秒；单次本地时长不代表生产部署压力测试。

## 逐条闭环

| 编号 / resultIndex | 规则 | 原位置 | 状态 | 当前依据 |
| --- | --- | --- | --- | --- |
| CQ-PY-001 / 0 | `py/polynomial-redos` | `ai-services/grading_agent/paper_compact/anchors.py:619` | fixed | 页脚候选在去掉首尾空白后先验证 80 字符上限；正则使用占有量词并把可选标点后的空白放入该分支，避免重复分配长空白。超长候选不按单页强页脚规则排除，原有跨页重复页边规则仍适用，不截断 OCR 证据；合法中文页码组合、候选边界与长失败输入均有回归验证。 |
| CQ-PY-002 / 1 | `py/attribute-shadows-method` | `services/subjective-grading-worker/subjective_grading_worker/runner.py:67` | fixed | 共享 LeaseHeartbeat 将回调存储字段改为 _send_callback，构造和后台续租都使用该字段，不再覆盖主观阅卷子类的 _send 方法；现有主观阅卷测试通过。 |
| CQ-PY-003 / 2 | `py/attribute-shadows-method` | `services/ocr-worker/ocr_worker/runner.py:363` | fixed | 共享 LeaseHeartbeat 将回调存储字段改为 _send_callback，不再覆盖 OCR 子类的 _send 方法；现有测试验证进度事件序号、命名参数以及处理过程中的续租。 |
| CQ-PY-004 / 3 | `py/attribute-shadows-method` | `services/page-processing-worker/page_processing/runner.py:512` | fixed | 共享 LeaseHeartbeat 将回调存储字段改为 _send_callback，不再覆盖页面处理子类的 _send 方法；现有测试验证同步首发、后台续租、立即/延迟进度与续租失败边界。 |
| CQ-PY-005 / 4 | `py/empty-except` | `services/ocr-worker/ocr_worker/deployment_profile.py:169` | fixed | 补充 /proc/cpuinfo 不可读时回退 platform.processor 或 unknown 的中文边界说明。 |
| CQ-PY-006 / 5 | `py/empty-except` | `services/ocr-worker/ocr_worker/deployment_profile.py:179` | fixed | 补充 cgroup v2 文件缺失、格式错误或零周期时继续尝试 v1 的中文边界说明，不把探测失败当作已知限额。 |
| CQ-PY-007 / 6 | `py/empty-except` | `services/ocr-worker/ocr_worker/deployment_profile.py:186` | fixed | 补充 cgroup v1 也不可用时返回 None 表示未知 CPU 限额的中文边界说明。 |
| CQ-PY-008 / 7 | `py/empty-except` | `services/ocr-worker/ocr_worker/deployment_profile.py:200` | fixed | 补充某种 cgroup 内存文件不可用时继续其他来源且只采纳有效上限的中文边界说明。 |
| CQ-PY-009 / 8 | `py/empty-except` | `services/ocr-worker/ocr_worker/deployment_profile.py:207` | fixed | 补充 /proc/meminfo 不可读时保留其他有效来源、没有有效来源以 0 表示未知的中文边界说明。 |
| CQ-PY-010 / 9 | `py/implicit-string-concatenation-in-list` | `docs/architecture/tools/build_module_catalog.py:59` | false_positive | 相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-011 / 10 | `py/implicit-string-concatenation-in-list` | `docs/architecture/tools/build_module_catalog.py:62` | false_positive | 相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-012 / 11 | `py/implicit-string-concatenation-in-list` | `docs/architecture/tools/build_module_catalog.py:64` | false_positive | 相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-013 / 12 | `py/implicit-string-concatenation-in-list` | `docs/architecture/tools/build_module_catalog.py:81` | false_positive | 相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-014 / 13 | `py/unnecessary-lambda` | `services/page-processing-worker/tests/test_main.py:59` | fixed | 测试将无参数转发 lambda 替换为 _config callable，保留一次模式的配置构造时机和行为。 |
| CQ-PY-015 / 14 | `py/unnecessary-lambda` | `services/page-processing-worker/tests/test_main.py:79` | fixed | 测试将无参数转发 lambda 替换为 _config callable，保留长期模式的配置构造时机和行为。 |
| CQ-PY-016 / 15 | `py/procedure-return-value-used` | `docs/architecture/tools/snapshot_workspace.py:48` | intentional | snapshot() 通过文件写入与 stdout 输出交付结果，返回 None；sys.exit(None) 在成功时退出 0，verify() 则返回 0/1。此处没有把过程返回值当业务数据使用，保留命令行契约。 |
| CQ-PY-017 / 16 | `py/import-and-import-from` | `services/page-processing-worker/tests/test_runner_state_boundaries.py:8` | intentional | runner_module 用于 monkeypatch 模块级依赖，Runner 名称用于构造对象。两种导入分别服务不同用途，现有页面处理相关测试通过，保留测试写法。 |
| CQ-PY-018 / 17 | `py/unused-import` | `docs/architecture/tools/build_database_specs.py:3` | fixed | build_database_specs.py 移除未使用的 re 与 collections 导入，保留 json、hashlib 以及现有生成行为；已验证语法，未运行会改写架构证据的生成器。 |
| CQ-PY-019 / 18 | `py/ineffectual-statement` | `services/ocr-worker/ocr_worker/engine.py:25` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-020 / 19 | `py/ineffectual-statement` | `services/ocr-worker/ocr_worker/engine.py:28` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-021 / 20 | `py/ineffectual-statement` | `services/ocr-worker/ocr_worker/math_layout.py:18` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-022 / 21 | `py/ineffectual-statement` | `services/ocr-worker/ocr_worker/math_layout.py:20` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-023 / 22 | `py/ineffectual-statement` | `services/ocr-worker/ocr_worker/math_layout.py:22` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-024 / 23 | `py/ineffectual-statement` | `ai-services/grading_agent/provider_adapter.py:13` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-025 / 24 | `py/ineffectual-statement` | `ai-services/grading_agent/provider_adapter.py:16` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-026 / 25 | `py/ineffectual-statement` | `ai-services/grading_agent/provider_adapter.py:19` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-027 / 26 | `py/ineffectual-statement` | `ai-services/grading_agent/provider_adapter.py:22` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-028 / 27 | `py/ineffectual-statement` | `services/ocr-worker/ocr_worker/recognition_router.py:76` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-029 / 28 | `py/ineffectual-statement` | `services/ocr-worker/ocr_worker/recognition_router.py:78` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-030 / 29 | `py/ineffectual-statement` | `services/ocr-worker/ocr_worker/recognition_router.py:80` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-031 / 30 | `py/ineffectual-statement` | `services/page-processing-worker/page_processing/runner.py:38` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-032 / 31 | `py/ineffectual-statement` | `services/page-processing-worker/page_processing/runner.py:41` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-033 / 32 | `py/ineffectual-statement` | `services/page-processing-worker/page_processing/runner.py:44` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-034 / 33 | `py/ineffectual-statement` | `services/image-quality-worker/image_quality/runner.py:16` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-035 / 34 | `py/ineffectual-statement` | `services/image-quality-worker/image_quality/runner.py:19` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-036 / 35 | `py/ineffectual-statement` | `services/image-quality-worker/image_quality/runner.py:22` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-037 / 36 | `py/ineffectual-statement` | `services/image-quality-worker/image_quality/runner.py:25` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-038 / 37 | `py/ineffectual-statement` | `services/image-quality-worker/image_quality/runner.py:28` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-039 / 38 | `py/ineffectual-statement` | `services/image-quality-worker/image_quality/runner.py:31` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-040 / 39 | `py/ineffectual-statement` | `services/image-quality-worker/image_quality/runner.py:34` | false_positive | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-041 / 40 | `py/unused-global-variable` | `services/ocr-worker/ocr_worker/math_layout.py:244` | false_positive | _adaptive_crop_formula 被 paper_formula.py 实际导入并调用，也由测试引用。公开实现仍在使用，告警不是裁剪/合并算法失效。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-042 / 41 | `py/unused-global-variable` | `services/ocr-worker/ocr_worker/math_layout.py:245` | intentional | _crop_formula = crop_formula 属于旧接口兼容别名。当前工作区搜索未找到本仓消费者，附近注释要求新代码使用公开名称；没有兼容期结束的依据，保留可导入接口。 |
| CQ-PY-043 / 42 | `py/unused-global-variable` | `services/ocr-worker/ocr_worker/math_layout.py:246` | false_positive | _merge_formula_boxes 被 test_paper_formula.py 实际导入并调用。公开实现仍在使用，告警不是裁剪/合并算法失效。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-044 / 43 | `py/unused-global-variable` | `services/ocr-worker/ocr_worker/math_layout.py:247` | intentional | _intersection_area = intersection_area 属于旧接口兼容别名。当前工作区搜索未找到本仓消费者，附近注释要求新代码使用公开名称；没有兼容期结束的依据，保留可导入接口。 |
| CQ-PY-045 / 44 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:19` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-046 / 45 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:20` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-047 / 46 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:24` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-048 / 47 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:27` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-049 / 48 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:28` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-050 / 49 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:29` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-051 / 50 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:30` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-052 / 51 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:33` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-053 / 52 | `py/unused-global-variable` | `ai-services/grading_agent/paper_compact/schema.py:36` | false_positive | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-054 / 53 | `py/unused-global-variable` | `docs/architecture/tools/verify_database_catalog.py:62` | false_positive | columns 在紧随其后的 FK 候选列表推导和唯一性生成器中被读取，参与字段与基数验证。不能把推导式内部引用当成全局变量从未使用。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |
| CQ-PY-055 / 54 | `py/unused-global-variable` | `docs/architecture/tools/verify_database_catalog.py:92` | false_positive | fk 在 any() 的生成器表达式中被读取（fk[2]/[3]/[4]/[6]），参与 SQLite 外键动作交叉核对。不能把推导式内部引用当成全局变量从未使用。 已重新核对当前源码与相关引用，保留该接口或实际使用逻辑。 |

完整机器可读数据、源文件指纹与验证记录见 [python-remediation.json](python-remediation.json)。
