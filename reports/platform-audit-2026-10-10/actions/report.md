# GitHub Actions 本地检查结果

本次没有触发、读取或扫描远端 GitHub 仓库，也没有创建 Actions run。用户要求扫描当前本地工作区，因此这里交付工作流文件的实际本地检查结果，不能据此断言远端 CI 全部通过。

## 实际执行结果

| 项目 | 结果 |
| --- | --- |
| 本地工作流文件 | `.github/workflows/ci.yml`、`workflow-lint.yml`、`release-images.yml` |
| 检查工具 | actionlint 1.7.7，仓库当前 CI 所使用的版本 |
| 镜像 | `rhysd/actionlint:1.7.7` |
| 本地镜像 ID | `sha256:887a259a5a534f3c4f36cb02dca341673c6089431057242cdc931e9f133147e9` |
| 执行隔离 | 源码只读挂载，`--network none`，1 CPU，256 MiB 内存上限 |
| 工具退出码 | 0 |
| 工具诊断 | 无 |
| GitHub Actions 平台运行结果 | 未执行 |

实际检查命令：

```powershell
docker run --rm --network none --read-only --cpus 1 --memory 256m -v 'D:\project\yuejuan:/repo:ro' -w /repo rhysd/actionlint:1.7.7
```

原始记录：`execution.json`、`actionlint-exit.txt`、`actionlint-version.txt`、`image-inspect.txt`。扫描期间其他工作修改了 `ci.yml`，因此 2026 年 10 月 11 日再次检查了全部三个工作流，仍为退出码 0、无诊断。`execution.json` 保存本次开始结束时间、命令与三个工作流的 SHA256；空的 `actionlint.log` 表示工具没有输出诊断。首次执行记录保留为 `initial-*`。

## 当前本地 CI 配置中存在的检查

`ci.yml` 已声明前端 lint、类型检查、单元测试、构建、模拟浏览器测试、接口与生成物契约检查、架构约束、Go 测试及竞态检查、六个关键 Go 包的覆盖率门禁、Python 静态与依赖安全检查、Python 测试和桌面 Rust 检查。工作流顶层权限为 `contents: read`，使用任务超时和同一工作流的并发取消机制。

这里的“存在”指工作流文件声明了任务，不能代替本次实际执行这些任务。各任务还依赖 runner 操作系统、固定工具链、数据库和容器等运行条件。

当前本地工作流没有 SonarQube、Semgrep、CodeQL 或 Codecov 的集成步骤。本次运行这些工具也没有修改工作流或提交扫描配置。

## 范围和解释

actionlint 对工作流语法、表达式、部分 action 输入以及脚本静态规则进行检查。无诊断不代表执行时的 npm、Go、Python、Rust、数据库与浏览器测试都会成功。

GitHub 自托管 runner 仍然执行来自 GitHub 的工作流任务；若要得到正式平台 run，需与 GitHub 服务连接。本次遵守本地限制，没有启动该流程。依据：[GitHub 自托管 runner 说明](https://docs.github.com/en/actions/concepts/runners/self-hosted-runners)。
