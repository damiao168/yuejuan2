# JavaScript / TypeScript CodeQL 修复闭环

原始 [javascript.sarif](../platform-audit-2026-10-10/codeql/javascript.sarif) 的 14 条结果全部逐条处理：**10 条修复、2 条有意行为、2 条模型误报、0 条 pending**。机器清单见 [javascript-remediation.json](javascript-remediation.json)，每条 `resultIndex` 对应原 SARIF 零起始索引。

原扫描证据未修改；本次针对当前工作区修复，保留原有未提交修改。没有运行全量 CodeQL 重扫，修复状态来自逐条实现复核及定向回归，不表示重新扫描后的告警计数。

| 原索引 | 原复核编号 | 结论 | 实际处理 |
| ---: | --- | --- | --- |
| 0 | CQ-JS-001 | fixed | 快照移除存在/类型预检查；已打开目录句柄枚举，文件 fstat 和读取使用同一描述符 |
| 1 | CQ-JS-002 | fixed | 已有生成物同一描述符比较、定位写入及截断；只读不变产物保留兼容 |
| 2 | CQ-JS-003 | fixed | 缺失生成物 wx+ 排他创建，EEXIST 有界重试，写失败不能返回成功 |
| 3 | CQ-JS-004 | fixed | 路由覆盖复用同一描述符生成写入，替换或失败时抛错 |
| 4 | CQ-JS-005 | fixed | 删除未用于任何断言的 examWorkspace 读取 |
| 5 | CQ-JS-006 | fixed | 删除未使用的 allUiSource 全 src 扫描，保留实际阅卷断言输入 |
| 6 | CQ-JS-007 | fixed | 删除重复 rubric 属性，保留末尾覆盖和空值回退，并补合成夹具回归 |
| 7 | CQ-JS-008 | intentional | 保留 Playwright run-code 的函数值输入，补 runner 注入 page 的说明 |
| 8 | CQ-JS-009 | intentional | 同上；维护文档、验收证据和现有 vm 函数执行回归一致 |
| 9 | CQ-JS-010 | false_positive | StorageEvent 两参数符合 DOM 类型；保留 key:null 存储清空事件 |
| 10 | CQ-JS-011 | false_positive | 保留 key 模拟跨标签页快捷键变更；误报来自 CodeQL 零参数 extern |
| 11 | CQ-JS-012 | fixed | 删除同一同步保存入口之后的重复租户/加载条件 |
| 12 | CQ-JS-013 | fixed | 第二处 !tenantID 删除；入口租户 guard 保留 |
| 13 | CQ-JS-014 | fixed | 第二处 loadingConfigs 删除；入口 state/ref 加载边界保留 |

生成写入保留已有 Windows `r+` 就地更新和权限行为。比较后定位从字节 0 写入，补齐短写，再截断旧尾部；新建使用排他打开。返回前核对打开文件与当前路径的 `dev/ino`，发现路径已替换或消失时抛错，避免把已脱离产物路径的写入报告为生成成功。该核对之后不会再按路径读取或写入。

符号链接仍由系统在打开时解析；描述符保证本次类型校验、读取与写入针对同一对象，并不提供不可信工作目录隔离。身份核对只检查当时观察到的路径状态，不锁定目录未来变更或同一文件内容的并发写入。就地写入失败会抛错并关闭句柄，不能承诺回滚已经写入的字节。

StorageEvent 的误报依据包含原 SARIF 的 `externs/web/webstorage.js` 零参数 relatedLocation、现有复核证据和本地 TypeScript `lib.dom.d.ts` 的 `new(type: string, eventInitDict?: StorageEventInit)` 声明。删除构造参数会损坏快捷键测试，因此保留原测试。

本次生产路由检查还发现已有 `normalizeBaseUrl` 改成线性 `trimmed.slice(0, end)` 后，断言仍硬编码 `return trimmed`。同步调整已修改脚本中的陈旧断言，检查空默认地址及保留空值，并用 11 个既有地址行为回归确认。没有改变 API 客户端业务实现。

| 验证 | 结果 |
| --- | --- |
| Node 生成文件竞争、契约、SDK 和 runner 定向回归 | 25 / 25 通过 |
| Lab 夹具、量规、证据及 schema 定向回归 | 37 / 37 通过 |
| Web 生产契约回归 | 17 / 17 通过 |
| 模型绑定及快捷键 Vitest | 14 / 14 通过 |
| 地址边界 Vitest | 11 / 11 通过 |
| 生产路由检查、定向 Biome、Web typecheck | 通过 |
| 真实 SDK、route-coverage 与当前源契约的只读生成比较 | 通过，未改生成物 |
| 本批文件 diff 空白检查 | 通过 |

共 104 个相关测试通过。生成文件回归在临时合成目录执行，包括真实打开后改名及替换路径、EEXIST 竞争、只读 fallback、UTF-8 字节定位、短写、尾部截断、权限与写失败关闭。重测试以单 worker 执行，避免放大本机内存压力。

本次涉及文件及实际维护点如下；这个清单只覆盖本次改动，未声称对未审读的仓库文件完成检查。

| 文件 | 本批状态 | 实际维护点 |
| --- | --- | --- |
| scripts/ci/check-generated-contracts.mjs | annotated | 目录及文件句柄顺序，路径解析与链接边界 |
| scripts/lib/openapi-sdk.mjs | annotated | 创建竞争、只读 fallback、字节位置、短写及成功/失败边界 |
| scripts/update-api-route-coverage.mjs | annotated | 复用描述符写入与错误传播 |
| scripts/ci/generated-files-race.test.mjs | annotated | 真实路径替换及临时合成回归 |
| apps/web-admin/scripts/check-production-routes.mjs | annotated | 删除死读取，修正陈旧同源断言并说明业务边界 |
| apps/web-admin/src/pages/PanelModelBindingsSection.tsx | existing_sufficient | 保留准确的同步 ref、租户和模型状态边界说明，删除重复条件 |
| lab/tests/helpers.js | annotated | 删除重复属性，解释空值回退及末尾覆盖 |
| lab/tests/helpers.test.js | annotated | 合成夹具覆盖与允许无效输入的测试边界 |
| docs/architecture/tools/capture_preview_qa.js | annotated | 外部 runner 函数输入用途 |
| docs/architecture/tools/render_native.js | annotated | 外部 runner 函数输入用途 |
| javascript-remediation.json / javascript-remediation.md | documentation | 14 条结果闭环与验证依据 |
