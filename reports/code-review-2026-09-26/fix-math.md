# 数学 Worker 修复验证（2026-09-27）

本次修复 F02、F03、F08、F09。修改仅涉及 `services/math-verification-worker`，未提交代码。

| 问题 | 最终处理 | 回归证据 |
| --- | --- | --- |
| F02 有限小数误判 | 所有数值 AST 使用精确 `sympy.Rational`，保留书面有限小数的精确语义。 | `0.1x+0.2x=0.3x`、纯常数加法和小数除法均 verified；相差 `10^-17` 的小数系数仍能区分。 |
| F03 非法实数子式被化简隐藏 | normalize、verify 与 solve 在构建最终表达式及等价捷径之前递归校验原始 AST；每个无自由变量子式必须确定为有限实数。verify、solve 额外从原始 AST 提取变量根式非负及幂的实数约束，无法解析时 uncertain；空定义域不认证。 | 零分母、负数平方根、两个非实平方根相乘抵消、含变量的隐藏非法子式、等式、零次幂和零乘法均拒绝；`sqrt(-x²)*sqrt(-x²)` 与 `-x²` 在 real / integer 下报告定义域不同，在 positive_real 下未定义；对应方程仅保留实数解 0。 |
| F08 大响应被错误超时 | 总预算从创建子进程前开始；显式 spawn 适配 HTTP 多线程；先在剩余预算内读取 Queue，再在剩余预算内 join。finally 中终止、必要时 kill，回收进程并关闭 Queue。 | 真实 spawn 传回超过 128 KiB 的序列化响应，与直接执行一致，未超时；真实超时、子进程异常、重复超时后再正常请求均无遗留活动子进程。 |
| F09 变量声明域被忽略 | integer 与 positive_real 映射为显式 Integers / `(0, +∞)`，与原始 AST 的实数定义域统一求交，用于分母零点、求解和等价比较；采样也必须落在该域内。域比较识别绑定变量重命名；无法证明相同或不同则 uncertain。 | 整数 `x²=2` 为空集、整数 `x²=4` 为两整数根、正实数只有正根且不含零；正实数 `x²=4 → x=2` verified；整数负幂与 `1/x` 等价；整数 `0*x^x=x+1` 保留解 -1；周期域相同不再因不同 Dummy 索引被误判。 |

最终执行：在 `D:/project/yuejuan/services/math-verification-worker` 下运行 `python -m pytest -q`，**52 passed in 27.14s**。`git diff --check -- services/math-verification-worker` 通过。测试使用本机 Python 和真实 multiprocessing spawn，未连接外部模型或数据库。

交叉复查新增 16 项回归，覆盖根式原始定义域、非整数及符号幂、0 的非正次幂、零次幂零点、整数负值与周期域等价。中间一次全量运行的子进程异常用例未在 10 秒预算内抛出预期异常；最终全量重跑在同样预算下通过，没有放宽测试时限。

边界：复杂约束、多变量定义域，以及无法解析的符号实数幂仍按既有契约返回 uncertain；幂沿用 SymPy 主值语义，0^0 保守视为未定义。超时后的资源清理另允许 terminate / kill 各最多一秒，以保证回收；没有把清理时间冒充计算预算。尚未执行部署镜像中的 Linux 测试。
