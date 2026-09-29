# AI / 数学验证最终审查裁决

本文件核对当前源码及 `math-worker-repro.py` / `math-worker-repro.log`，未改业务代码，未重跑完整测试。四类问题的根因独立：数值表示、无变量定义域、声明变量全集、进程通信顺序。前两项已沿当前生产调用链核对；第三项影响范围只限 worker 暴露的声明变量接口，不能据此断言当前 OCR 已传入整数或正实数条件。

## AI-1 — [P1] 十进制字面量转为浮点，正确变形被确定性判错

- 精确定位：`services/math-verification-worker/math_verification_worker/verifier.py:107`。连锁位置为 `:69–74`、`:176–180`。
- 复现：`verify_equivalence(parse("0.1x+0.2x"), parse("0.3x"))` 返回 `contradicted / numeric_counterexample / x=-3`，见 `math-worker-repro.log:5`。本次又直接调用生产使用的 `verify_transition`，得到同样判错。
- 根因：有限小数字面量被构造成 `sympy.Float`，0.1 + 0.2 与 0.3 留下舍入差；代数检查和反例检查均要求精确为零，舍入误差遂成为“数学反例”。
- 生产调用链：`ocr_worker/math_runner.py:24–27` 固定实数域调用 `/internal/math/verify-transition` → `server.py:36–37,98` → `verifier.py:78–82`；`symbolic_runner.py:105–108,146–157` 将结果保存为置信度 1 的 equivalence。后端 `mathunderstanding/rubric.go:186–192` 在题目目标、识别置信度、推导边及前序证据符合条件时将 contradicted 用为扣分证据；`scorer.go:203–206` 给该点 0 分。这是错误评分建议，不是绕过教师确认：`scorer.go:132–133` 仍要求人工复核。
- 修复建议：将解析器接受的有限十进制字符串同样转为 `sympy.Rational(value)`，保留题面精确数值语义；若未来显式支持近似测量值，单独定义近似比较契约，不把浮点非零差直接当作可证明反例。回归包括小数合并、十进制方程和真正不等式。

## AI-2 — [P1] 常量表达式跳过原始 AST 定义域校验，非实数运算获“实数等价”认证

- 精确定位：`services/math-verification-worker/math_verification_worker/verifier.py:40–53`，尤其 `:42` 只对恰好一个变量调用 `_defined_domain`。
- 复现：`1/0 → 2/0` 和 `sqrt(0-1) → 2sqrt(0-1)/2` 均返回 `verified / equivalent_transform / domain=real`，见日志第 3–4 行。补充直接复现 `sqrt(0-1)sqrt(0-1) → 0-1` 也返回相同 verified 结果：原式在实数域无定义，目标却是合法实常量 -1。因此问题不局限于“两边都无定义”的形式。
- 根因：零变量时两边定义域直接保留 `S.Reals`，SymPy 可把除零折叠为 `zoo`、把负数根式折叠为 `I`，并在原始 AST 验证前完成化简。随后 `left == right` 无条件认证，丢失非法中间子式。只检查最终结果是否实数也不足以修复 `sqrt(-1)*sqrt(-1)`。
- 生产调用链与 AI-1 相同；`symbolic_runner.py:156` 为其写入置信度 1。`rubric.go:189–190` 在目标与其他证据门槛满足时可将其作为 supported 证据，`scorer.go:198–202` 将该点计入已验证分数。仍属于需教师确认的评分建议。
- 修复建议：独立遍历原始 AST，对零变量表达式及其每个子式检查定义性和实数域有效性，在任何代数相等快速路径前处理；除零、负实数开偶次根及非有限常量至少返回 uncertain/unsupported，不能认证实数等价。加入“非法常量子式化简为合法最终常量”的回归。

## AI-3 — [P2] 变量 integer / positive_real 声明未约束实际求解全集

- 精确定位：`services/math-verification-worker/math_verification_worker/verifier.py:136–150`（`result = S.Reals` 及求定义域始终使用 `S.Reals`）；调用点 `:55–56,93–96`；接收声明的位置 `:154–165`。
- 复现：`solve(x^2=2, x, variables={x:integer})` 返回 verified 的 `{-sqrt(2), sqrt(2)}`，预期空集；`verify_transition(x^2=4, x=2, variables={x:positive_real})` 返回 contradicted / solutions_lost，预期等价，见日志第 1–2 行。
- 根因：符号上有 assumptions 不等于求解器的定义域；符号声明虽然被接收，`solveset` 仍获实数全集。定义域、求解和化简因此使用不一致的语义。
- 影响限定：这是认证内部 HTTP worker API 的真实错误：`server.py:26–28,35–39` 接收并转交 domain；现有 `ocr_worker/math_runner.py:21–30` 只发送 `{domain: real}`，未传 `variables`。本轮未发现当前生产调用者会触发上述声明分支，不应把本项写成现有整数题普遍误判，也不应把“客户端不传题目条件”混成同一个已复现缺陷。
- 修复建议：将声明转换为显式全集（整数集、开区间 `(0,+∞)` 等），与原 AST 定义域求交，并在求解、等价比较和采样反例中一致使用。若当前不准备支持，拒绝该变量声明并返回 uncertain，而不是接受后作确定性判断。

## AI-4 — [P2] 先 join 子进程再读取 Queue，大响应完成计算仍超时

- 精确定位：`services/math-verification-worker/math_verification_worker/server.py:51–61`，关键顺序为 `process.join(timeout_seconds)` 在 `queue.get_nowait()` 之前。
- 现有日志精确数据：512 叶平衡 AST 请求 58,837 字节，小于 HTTP 的 262,144 字节上限（`:19,83`）；结果 pickle 78,417 字节；直接执行 250 ms；15 秒 bounded 配置下墙钟 20,719 ms，返回 verification_timeout。相同 `_process_entry` 和输入改成先 `queue.get(timeout=30)` 再 join，得到完全相同结果且子进程退出（5,484 ms）。见 `math-worker-repro.log:7–8`。不要引用旧版本“85 KB / 16 ms / 22 秒”的数值。
- 根因：子进程 `queue.put` 后退出时等待 feeder 把数据写完；结果超过平台管道缓冲时 feeder 等待读取方，而父进程正在等待子进程退出。时间用于相互等待而非符号计算。进程启动开销解释了墙钟超出 join 的 15 秒，但不影响因果判断；先读再 join 的对照消除了输入计算过慢解释。
- 影响限定：所有 HTTP 运算均经 `server.py:98` 使用该包装器。已严格复现的是 `/normalize` 支持的直接 AST 输入，大输入合法且结果丢失；当前 OCR client 的 normalize 使用 LaTeX 字符串，受解析器 4,096 字符 / 1,024 token 限制。本轮没有证明该特定 512 叶样例能从当前 OCR LaTeX 链进入，也未测定所有部署平台的缓冲阈值。不要称为“所有请求必超时”。
- 修复建议：父进程在总截止时间内主动读取队列/管道，再 join 已完成子进程；超时路径终止并回收，显式关闭资源，不以 `Queue.empty()` 判定跨进程完成。增加结果大小超过管道缓冲的回归，以同一子进程目标和相同 payload 验证不会误超时。

## 证据与验证边界

- 前轮汇报测试通过数：AI 113、OCR 96（另有 5 subtests）、页面处理 63、图像质量 32、主观题 worker 7、数学验证 12、Lab 132；均早于外部后续 AI / paper / subjective 并行修改。本次未将这些结果描述为当前工作区全量通过。
- 本次新增小复现仅在 Python 中直接 import 当前 worker，结果为：`sqrt(0-1)sqrt(0-1) → 0-1` 返回 verified；`0.1x+0.2x → 0.3x` 返回 contradicted，均通过生产 `verify_transition` 调用。
- 排序建议：主报告保留 AI-1、AI-2；AI-4 作为 worker 可用性独立问题；AI-3 保留明确内部接口范围，优先级低于已接入生产链的数学判定错误。
