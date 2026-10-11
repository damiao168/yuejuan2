# CodeQL Python 质量告警人工核验

本附件逐条核验 `python.sarif` 中的 54 条质量候选；另 1 条 `py/polynomial-redos` 安全候选由主 CodeQL 报告单独处理，未混入本附件统计。自动告警的 error/note 等级保留，人工判断另列。

固定快照：`D:\tools\audit-codeql-20261010\source-20261011`，CodeQL 2.27.2。不以当前工作区的后续修复将快照告警判为误报；所有命中文件 SHA256 均与清单一致。

原始 SARIF SHA256：`1d049209dfb252a3022af8738ea19adccbb0388b17d9dac8bb084a54c17ea45b`。快照源清单 SHA256：`f939ec3a24e837f45fba766fc1d614ad75d63707d8303e2f090029787506a56f`。

## 核验统计

| 分类 | 数量 | 含义 |
| --- | ---: | --- |
| 上下文误报 | 39 | 接口占位、跨文件消费者、推导式引用或有意段落拼接 |
| 有意实现／兼容行为 | 7 | 现有语义可解释；可选命名/兼容接口整理 |
| 低影响维护项 | 8 | 注释或写法应改进，不是已确认生产漏洞 |

54 条告警上下文均已核验，无待核验条目；其中没有确认会导致运行错误的质量缺陷。这不代表全仓库人工审读完成，也不证明阅卷正确率、续租并发或权限边界已通过集成测试。

| 规则 | 原始候选数 |
| --- | ---: |
| `py/attribute-shadows-method` | 3 |
| `py/empty-except` | 5 |
| `py/implicit-string-concatenation-in-list` | 4 |
| `py/unnecessary-lambda` | 2 |
| `py/procedure-return-value-used` | 1 |
| `py/import-and-import-from` | 1 |
| `py/unused-import` | 1 |
| `py/ineffectual-statement` | 22 |
| `py/unused-global-variable` | 15 |

## 主要判断及边界

5 条空 except 位于 OCR 可选硬件描述探测：Linux /proc 或 cgroup 文件缺失、限额格式不可用时允许回退。现有分支缺少允许忽略及回退边界说明，属于低影响维护问题。建议按仓库注释标准补充说明，必要时提供不含敏感值的 debug 诊断；无需把正常跨平台缺失升级为致命错误。

3 条 error 级方法遮蔽均为 LeaseHeartbeat 回调注入：子类绑定方法先传入父类，随后父类将同一 callable 存在 `_send` 字段。用固定快照 AST 选取真实类定义和构造器、假 API 与类型桩，三个对象的 `_send` 都仍绑定到同一实例及子类方法，各成功触发一次假 API 调用；未导入应用、未启动线程、未联网。这仅验证构造和派发语义；不替代续租超时及并发测试。建议后续将存储字段命名为 `_send_callback` 或解释注入关系，减少维护困惑。

22 条无效语句命中 typing.Protocol 接口方法中的 `...`；它们用于类型接口声明。9 条 schema 正则与 2 条数学兼容别名在其他快照文件有实际消费者；2 条数据库脚本变量被推导式／生成器读取。不能仅据定义文件内无引用而删除这些内容。

剩余 2 个数学兼容别名在该快照没有搜索到消费者，但附近注释明确保留兼容接口：当前核验只接受这一有限兼容用途，未证明外部依赖存在，也未建议无条件保留永久死代码。

4 条字符串拼接属于连续说明段落；1 条 snapshot() 返回值只是交给 sys.exit(None) 使用成功状态；测试同时导入模块和 Runner 分别用于 monkeypatch 与实例构造。真实但低影响的另外 3 条写法问题是 2 个冗余测试 lambda 和 1 条合并了 re/collections 两项的未使用 import 告警。

## 逐条对应

编号对应 SARIF 结果的一基序号；CQ-PY-001 是单独处理的安全候选。JSON 同时保存零基索引、原始等级/消息、完整证据文件哈希与审读行段。表中位置均指固定快照。

| 编号 | 原始规则／等级 | 快照位置 | 人工判断 | 依据与建议 |
| --- | --- | --- | --- | --- |
| CQ-PY-002 | `py/attribute-shadows-method` / error | `services/subjective-grading-worker/subjective_grading_worker/runner.py:67` | 有意实现／兼容行为 | 构造器先取得 self._send 的子类绑定方法，再传给父类；父类把同一 callable 存入实例 _send。确有名称遮蔽，但调用仍绑定同一对象和子类实现，不是续租回调被非 callable 覆盖。隔离构造核验三个类均派发到假 API，未启动线程。<br>建议：可在后续维护中把父类存储字段改为 _send_callback，或补充回调注入说明，减少非直观名称遮蔽；本次无需按运行故障修复。 |
| CQ-PY-003 | `py/attribute-shadows-method` / error | `services/ocr-worker/ocr_worker/runner.py:363` | 有意实现／兼容行为 | 构造器先取得 self._send 的子类绑定方法，再传给父类；父类把同一 callable 存入实例 _send。确有名称遮蔽，但调用仍绑定同一对象和子类实现，不是续租回调被非 callable 覆盖。隔离构造核验三个类均派发到假 API，未启动线程。<br>建议：可在后续维护中把父类存储字段改为 _send_callback，或补充回调注入说明，减少非直观名称遮蔽；本次无需按运行故障修复。 |
| CQ-PY-004 | `py/attribute-shadows-method` / error | `services/page-processing-worker/page_processing/runner.py:512` | 有意实现／兼容行为 | 构造器先取得 self._send 的子类绑定方法，再传给父类；父类把同一 callable 存入实例 _send。确有名称遮蔽，但调用仍绑定同一对象和子类实现，不是续租回调被非 callable 覆盖。隔离构造核验三个类均派发到假 API，未启动线程。<br>建议：可在后续维护中把父类存储字段改为 _send_callback，或补充回调注入说明，减少非直观名称遮蔽；本次无需按运行故障修复。 |
| CQ-PY-005 | `py/empty-except` / note | `services/ocr-worker/ocr_worker/deployment_profile.py:169` | 低影响维护项 | /proc/cpuinfo 不可用时回退 platform.processor/unknown。这是可选硬件描述探测，不是吞掉成绩写入或权限校验异常；但 except 分支缺少解释，权限/格式错误和正常跨平台缺失无法直接区分，符合低影响可维护性告警。<br>建议：在 except 内用简短中文说明允许忽略的探测失败和后续回退；如需诊断仅记录不含敏感值的 debug 原因，避免把缺失探测强行当成已知容量。 |
| CQ-PY-006 | `py/empty-except` / note | `services/ocr-worker/ocr_worker/deployment_profile.py:179` | 低影响维护项 | cgroup v2 CPU 限额探测失败时继续尝试 v1。这是可选硬件描述探测，不是吞掉成绩写入或权限校验异常；但 except 分支缺少解释，权限/格式错误和正常跨平台缺失无法直接区分，符合低影响可维护性告警。<br>建议：在 except 内用简短中文说明允许忽略的探测失败和后续回退；如需诊断仅记录不含敏感值的 debug 原因，避免把缺失探测强行当成已知容量。 |
| CQ-PY-007 | `py/empty-except` / note | `services/ocr-worker/ocr_worker/deployment_profile.py:186` | 低影响维护项 | cgroup v1 探测失败时返回 None 表示未知限额。这是可选硬件描述探测，不是吞掉成绩写入或权限校验异常；但 except 分支缺少解释，权限/格式错误和正常跨平台缺失无法直接区分，符合低影响可维护性告警。<br>建议：在 except 内用简短中文说明允许忽略的探测失败和后续回退；如需诊断仅记录不含敏感值的 debug 原因，避免把缺失探测强行当成已知容量。 |
| CQ-PY-008 | `py/empty-except` / note | `services/ocr-worker/ocr_worker/deployment_profile.py:200` | 低影响维护项 | cgroup 内存限制不可用时继续读取 /proc/meminfo。这是可选硬件描述探测，不是吞掉成绩写入或权限校验异常；但 except 分支缺少解释，权限/格式错误和正常跨平台缺失无法直接区分，符合低影响可维护性告警。<br>建议：在 except 内用简短中文说明允许忽略的探测失败和后续回退；如需诊断仅记录不含敏感值的 debug 原因，避免把缺失探测强行当成已知容量。 |
| CQ-PY-009 | `py/empty-except` / note | `services/ocr-worker/ocr_worker/deployment_profile.py:207` | 低影响维护项 | 主机内存不可读时使用其他已收集候选，全部不可用才返回 0。这是可选硬件描述探测，不是吞掉成绩写入或权限校验异常；但 except 分支缺少解释，权限/格式错误和正常跨平台缺失无法直接区分，符合低影响可维护性告警。<br>建议：在 except 内用简短中文说明允许忽略的探测失败和后续回退；如需诊断仅记录不含敏感值的 debug 原因，避免把缺失探测强行当成已知容量。 |
| CQ-PY-010 | `py/implicit-string-concatenation-in-list` / warning | `docs/architecture/tools/build_module_catalog.py:59` | 上下文误报 | 相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。<br>建议：保留输出语义；可用括号或显式拼接表达段落边界以减少告警，不能直接在每行末尾加逗号改变文档段落。 |
| CQ-PY-011 | `py/implicit-string-concatenation-in-list` / warning | `docs/architecture/tools/build_module_catalog.py:62` | 上下文误报 | 相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。<br>建议：保留输出语义；可用括号或显式拼接表达段落边界以减少告警，不能直接在每行末尾加逗号改变文档段落。 |
| CQ-PY-012 | `py/implicit-string-concatenation-in-list` / warning | `docs/architecture/tools/build_module_catalog.py:64` | 上下文误报 | 相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。<br>建议：保留输出语义；可用括号或显式拼接表达段落边界以减少告警，不能直接在每行末尾加逗号改变文档段落。 |
| CQ-PY-013 | `py/implicit-string-concatenation-in-list` / warning | `docs/architecture/tools/build_module_catalog.py:81` | 上下文误报 | 相邻字符串是连续中文说明段落，首段与 Markdown 链接/后续限制说明应合成一个列表元素；逗号明确位于段落末尾。未见需要生成独立列表项的缺失逗号。<br>建议：保留输出语义；可用括号或显式拼接表达段落边界以减少告警，不能直接在每行末尾加逗号改变文档段落。 |
| CQ-PY-014 | `py/unnecessary-lambda` / note | `services/page-processing-worker/tests/test_main.py:59` | 低影响维护项 | 测试 monkeypatch 使用无参数 lambda: _config()，只转调本文件默认配置工厂；本测试范围内没有替换 _config 的后续步骤。告警为真实但低影响的测试写法冗余，不影响阅卷运行。<br>建议：可把这两处 lambda 换成 _config；带自定义参数的其他 lambda 不属于此结论。 |
| CQ-PY-015 | `py/unnecessary-lambda` / note | `services/page-processing-worker/tests/test_main.py:79` | 低影响维护项 | 测试 monkeypatch 使用无参数 lambda: _config()，只转调本文件默认配置工厂；本测试范围内没有替换 _config 的后续步骤。告警为真实但低影响的测试写法冗余，不影响阅卷运行。<br>建议：可把这两处 lambda 换成 _config；带自定义参数的其他 lambda 不属于此结论。 |
| CQ-PY-016 | `py/procedure-return-value-used` / warning | `docs/architecture/tools/snapshot_workspace.py:48` | 有意实现／兼容行为 | snapshot() 写基线并打印摘要，没有返回值；其 None 仅传给 sys.exit，Python 对 sys.exit(None) 使用成功退出状态。--verify 分支则保留 verify() 的 0/1 返回值。没有把 None 当作快照数据使用。<br>建议：可让 snapshot() 显式 return 0 或分开两条 sys.exit 分支以提高清晰度；不应把此告警描述为基线校验失效。 |
| CQ-PY-017 | `py/import-and-import-from` / note | `services/page-processing-worker/tests/test_runner_state_boundaries.py:8` | 有意实现／兼容行为 | Runner 类用于构造和类型注解，runner_module 模块对象用于 monkeypatch 其依赖函数。两种导入承担不同测试职责，不是重复执行模块或导错对象。<br>建议：可统一通过 runner_module.Runner 引用以减少风格告警；保留双导入也不会产生已证实的运行问题。 |
| CQ-PY-018 | `py/unused-import` / note | `docs/architecture/tools/build_database_specs.py:3` | 低影响维护项 | 同一 import 语句中的 re 和 collections 在固定快照文件后续均无引用。SARIF 把两项 unused import 合并成一条结果；不要计作两条漏洞。工具只生成架构文档，影响为多余依赖和可读性。<br>建议：后续清理该语句里的 re、collections，保留实际使用的 json、hashlib；本次不修改生成工具。 |
| CQ-PY-019 | `py/ineffectual-statement` / note | `services/ocr-worker/ocr_worker/engine.py:25` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-020 | `py/ineffectual-statement` / note | `services/ocr-worker/ocr_worker/engine.py:28` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-021 | `py/ineffectual-statement` / note | `services/ocr-worker/ocr_worker/math_layout.py:18` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-022 | `py/ineffectual-statement` / note | `services/ocr-worker/ocr_worker/math_layout.py:20` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-023 | `py/ineffectual-statement` / note | `services/ocr-worker/ocr_worker/math_layout.py:22` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-024 | `py/ineffectual-statement` / note | `ai-services/grading_agent/provider_adapter.py:13` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-025 | `py/ineffectual-statement` / note | `ai-services/grading_agent/provider_adapter.py:16` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-026 | `py/ineffectual-statement` / note | `ai-services/grading_agent/provider_adapter.py:19` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-027 | `py/ineffectual-statement` / note | `ai-services/grading_agent/provider_adapter.py:22` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-028 | `py/ineffectual-statement` / note | `services/ocr-worker/ocr_worker/recognition_router.py:76` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-029 | `py/ineffectual-statement` / note | `services/ocr-worker/ocr_worker/recognition_router.py:78` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-030 | `py/ineffectual-statement` / note | `services/ocr-worker/ocr_worker/recognition_router.py:80` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-031 | `py/ineffectual-statement` / note | `services/page-processing-worker/page_processing/runner.py:38` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-032 | `py/ineffectual-statement` / note | `services/page-processing-worker/page_processing/runner.py:41` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-033 | `py/ineffectual-statement` / note | `services/page-processing-worker/page_processing/runner.py:44` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-034 | `py/ineffectual-statement` / note | `services/image-quality-worker/image_quality/runner.py:16` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-035 | `py/ineffectual-statement` / note | `services/image-quality-worker/image_quality/runner.py:19` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-036 | `py/ineffectual-statement` / note | `services/image-quality-worker/image_quality/runner.py:22` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-037 | `py/ineffectual-statement` / note | `services/image-quality-worker/image_quality/runner.py:25` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-038 | `py/ineffectual-statement` / note | `services/image-quality-worker/image_quality/runner.py:28` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-039 | `py/ineffectual-statement` / note | `services/image-quality-worker/image_quality/runner.py:31` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-040 | `py/ineffectual-statement` / note | `services/image-quality-worker/image_quality/runner.py:34` | 上下文误报 | 命中表达式为 typing.Protocol 接口方法体的 ...，用于声明结构化接口；不是应执行副作用的业务实现遗漏。该位置所在类明确继承 Protocol。<br>建议：保留接口声明；如配置质量门禁，可对 Protocol 方法中的省略号作精确规则例外，不要把全部空方法排除。 |
| CQ-PY-041 | `py/unused-global-variable` / note | `services/ocr-worker/ocr_worker/math_layout.py:244` | 上下文误报 | _adaptive_crop_formula 被 paper_formula.py 实际导入并调用，也由测试引用。公开实现仍在使用，告警不是裁剪/合并算法失效。<br>建议：有实际消费者的别名应保留或先迁移调用方；无本仓消费者的兼容别名在确认兼容期结束后才删除。 |
| CQ-PY-042 | `py/unused-global-variable` / note | `services/ocr-worker/ocr_worker/math_layout.py:245` | 有意实现／兼容行为 | 固定快照搜索未找到此兼容别名的实际消费者；邻近注释明确将它作为旧接口兼容别名保留。仅凭本仓库没有使用者不能证明不存在旧调用方。公开实现仍在使用，告警不是裁剪/合并算法失效。<br>建议：有实际消费者的别名应保留或先迁移调用方；无本仓消费者的兼容别名在确认兼容期结束后才删除。 |
| CQ-PY-043 | `py/unused-global-variable` / note | `services/ocr-worker/ocr_worker/math_layout.py:246` | 上下文误报 | _merge_formula_boxes 被 test_paper_formula.py 实际导入并调用。公开实现仍在使用，告警不是裁剪/合并算法失效。<br>建议：有实际消费者的别名应保留或先迁移调用方；无本仓消费者的兼容别名在确认兼容期结束后才删除。 |
| CQ-PY-044 | `py/unused-global-variable` / note | `services/ocr-worker/ocr_worker/math_layout.py:247` | 有意实现／兼容行为 | 固定快照搜索未找到此兼容别名的实际消费者；邻近注释明确将它作为旧接口兼容别名保留。仅凭本仓库没有使用者不能证明不存在旧调用方。公开实现仍在使用，告警不是裁剪/合并算法失效。<br>建议：有实际消费者的别名应保留或先迁移调用方；无本仓消费者的兼容别名在确认兼容期结束后才删除。 |
| CQ-PY-045 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:19` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-046 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:20` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-047 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:24` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-048 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:27` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-049 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:28` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-050 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:29` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-051 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:30` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-052 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:33` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-053 | `py/unused-global-variable` / note | `ai-services/grading_agent/paper_compact/schema.py:36` | 上下文误报 | 该正则常量通过同包 anchors.py/chunks.py 的显式导入被实际匹配、搜索或迭代调用。schema.py 内部没有读取不等于整个程序没有使用；删除会破坏 OCR 锚点或分块逻辑。<br>建议：保留实际被导入的常量；可集中定义明确的共享接口或使用精确规则抑制。此质量结论不消除另一条 py/polynomial-redos 安全告警。 |
| CQ-PY-054 | `py/unused-global-variable` / note | `docs/architecture/tools/verify_database_catalog.py:62` | 上下文误报 | columns 在紧随其后的 FK 候选列表推导和唯一性生成器中被读取，参与字段与基数验证。不能把推导式内部引用当成全局变量从未使用。<br>建议：保留验证逻辑；如为可读性把脚本主体封装为 main()，需确认行为相同，不能直接删除这两项赋值。 |
| CQ-PY-055 | `py/unused-global-variable` / note | `docs/architecture/tools/verify_database_catalog.py:92` | 上下文误报 | fk 在 any() 的生成器表达式中被读取（fk[2]/[3]/[4]/[6]），参与 SQLite 外键动作交叉核对。不能把推导式内部引用当成全局变量从未使用。<br>建议：保留验证逻辑；如为可读性把脚本主体封装为 main()，需确认行为相同，不能直接删除这两项赋值。 |

## 复现与交付范围

`review_python_quality.py` 保存本次人工判断映射和隔离构造核验；执行它只读固定快照及原始 SARIF，并重生成本附件 JSON/Markdown，不修改业务源码。人工判断映射不会自动证明将来的源码版本；快照哈希变化时需要重新审读。

本附件只负责 54 条 Python 质量候选，不修改主 CodeQL README、summary 或 alerts.csv。安全正则、JavaScript/Actions/Go/Rust 引擎结果以主报告为准。
