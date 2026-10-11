# 最终复扫前：独立交付证据审计

原导出/总账：2223/2223，原 key 缺失 0、重复 0。新增 checkpoint02 47 项，root/Python/Go 各 {'root-new-findings-progress.json': 23, 'python-new-findings-progress.json': 19, 'go-new-findings-progress.json': 5}；缺失 0、重复 0。

最终平台状态仍待 checkpoint03 完整扫描及导出，不能从源码状态推断告警归零。Go 交接引用重叠按原总账单一 owner 计算，不重复累计。所有逐文件 partial/pending/protected 状态原样保留，本审计不把哈希或枚举当全文审读。

当前版本证据注意点：

- **PROGRESS_SOURCE_SHA_DRIFT**：负责报告记录的源码版本与当前字节有差异；历史记录保持原样，最终交付需新证据绑定当前版本。
- **FRONTEND_EARLY_RESULTS_NOT_REPLAYABLE_ARTIFACTS**：部分早期通过记录只有会话输出引用，无原日志/SHA；后续 web 38/416 已有当前绑定，不应将早期记录累加成新增通过数。其它应用类型检查仍有已明确保留的资源失败待补记录。
- **PYTHON_CURRENT_TEST_SHA_NOT_DYNAMically_BOUND**：原总测试报告仍有 3 个测试源 SHA 漂移；静态语法/语义等价审读可保留，当前实际执行证明不能从旧 SHA直接转移。
- **PREVIEW_CHANGE_PROVENANCE_INACCURATE**：root 新告警说明把 build_preview XML 修改归于本 Python 分工，但本智能体未修改该 root 独占文件，之前双方已确认外部未知变更需保留。该归因不能替代 SHA/真实验证证据。

model 实际静态导入范围（仅根包测试+依赖生产源码）117 文件；真实编译输入 SHA 漂移 0 文件，其余 manifest 扩大的依赖测试变更不要求重验 model。详见 JSON 的 import_chain。

最小复验：若 model 生产依赖确有变更，当前包一次；Python 若无后续 current 绑定，3 个指定测试文件；server 首次动态源存在后续修改，正在进行的第二批验证应绑定当前版本；前端类型/静态门禁与 E2E 的原资源失败不计通过。无需重复全部已通过测试。

原 Go 条件 skip 独立保存；没有发现把 skip 加到 pass 的记录。嵌套测试计数需按实际测试/子测试陈述，不宣称相同数量独立业务场景或真实数据库/模型全部验收。

完整 key、当前 SHA、旧证据差异、逐文件待审范围、JUnit/JSONL 数量与所有输入报告 SHA 在同名 JSON；动态过程中有其它智能体写报告的稳定性也逐项记录。
