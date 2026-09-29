# 注释过程中发现的现有逻辑问题

本次只补注释，以下代码保持原样，后续修复应作为独立变更验证。

1. `services/api-gateway/internal/releasegate/service.go` 的 `cloneGate` 先将 `value.Counts` 替换为空 map，再遍历该 map，因此返回副本丢失原有计数。应从原 map 复制到新 map，并用非空计数验证副本内容及隔离性。
2. `services/api-gateway/internal/logger/logger.go` 的 `New` 保存日志级别，但 `write` 未读取该配置；当前 `Info`、`Warn`、`Error` 都会进入输出。后续明确级别策略并验证过滤结果。
3. `apps/desktop-client/src-tauri/src/scanner.rs` 的 `available_disk_bytes` 读取所有文件系统盘中最大的剩余容量；预检文案却表示满足扫描 spool 的最低空间要求。spool 盘满而其他盘有空间时可能误通过。应基于实际 spool 路径定位卷并验证多盘情形。
4. `scripts/lib/openapi-sdk.mjs` 的 `normalizeSchema` 将 `properties` 字段映射也当作 schema 用关键字白名单过滤，普通属性名会丢失。只读构造 `Example.value` 从 string 改为 number，生成的两份基线均为 `properties: {}`，`findBreakingChanges` 返回空数组。现有直接构造基线的单元测试未覆盖这条提取路径；后续应修复属性映射递归并增加“原始契约→基线→差异”测试，再评审基线更新。
