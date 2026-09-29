# 修复交叉复查（2026-09-27）

本轮复查范围为数学 Worker 的原始实数定义域与客户端离线状态读取；未修改客户端代码，未提交代码。

## 数学定义域

复查发现 `sqrt(-x²)*sqrt(-x²)` 先被 SymPy 化简为 `-x²`，连续定义域检查无法恢复原根式限制。现在递归保留原始 AST 的根式非负、幂和分母约束，再统一与变量声明域求交；不能确定的符号幂返回 uncertain。0^0 和 0 的负次幂保守拒绝。

同时修复两个边界：整数负幂与分式不再因为未化简集合形式不同而误判；周期函数定义域中不同的绑定索引不会被误认为不同取值域。比较仍不能证明相同或不同则返回 uncertain。

验证命令为 `python -m pytest -q`，最终 **52 passed in 27.14s**；使用真实 multiprocessing spawn 验证超过管道缓冲区的大响应、超时清理及异常回收。原始 36 项与新增 16 项均通过。

## 客户端只读确认

`apps/desktop-client/src/components/OfflineWorkbench.tsx` 的 `loadDraft` 并行读取加密草稿和最新 envelope 元数据，使用元数据的 `syncStatus` / `syncMessage` 覆盖密文中提交前的旧状态。内存中已经收到服务端确认的任务继续保持 synced。

`OfflineWorkbench.test.tsx` 的 `loads the latest status metadata instead of the encrypted pre-submit status` 用例明确让密文保留 syncing、元数据变为 synced，再卸载并重新挂载组件、加载草稿，检查禁止重复提交和覆盖已同步草稿。该项在本轮只读确认，运行结果由客户端修复负责人提供。
