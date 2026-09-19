# EduGrade TypeScript SDK

这个包向 Web 和桌面端提供基于 API Gateway OpenAPI 契约的类型与调用方法。`src/generated/` 由生成脚本维护，请勿手工修改；`src/assessment.ts` 等领域辅助方法保持轻量，只负责组合已定义的接口。

生成的 `EduGradeApi` 只包含 OpenAPI 契约中存在的操作。调用方提供 `ApiTransport`，继续沿用自身的认证、CSRF 和幂等处理；SDK 不替调用方管理这些状态。

## 生成与检查

在仓库根目录运行：

```powershell
npm run generate:sdk
npm run check:openapi-breaking
```

CI 会重新生成 SDK，并在生成文件与提交内容不一致时失败。兼容性检查以 [`contracts/openapi/edugrade-api.breaking-baseline.json`](../../contracts/openapi/edugrade-api.breaking-baseline.json) 为基线，阻止删除操作或结构、增加必填参数或属性、改变类型或引用，以及删除枚举值。

如果 API 的破坏性变更已经过评审，应在同一次变更中更新兼容性基线，并检查生成文件和使用方：

```powershell
npm run openapi:baseline
```
