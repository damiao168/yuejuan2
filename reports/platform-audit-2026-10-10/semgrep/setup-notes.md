# Semgrep 本地工具准备记录

- 2026-10-10 下载官方 `semgrep/semgrep:latest`；镜像最终完成，固定 digest 为 `sha256:93963d9295a366f59e4850127b1550400ee7b388f04fe144e4a1f6325d96e01b`。下载记录见 `docker-pull.log`。
- 同期尝试在现成 Python Slim 容器中隔离安装官方 PyPI Semgrep，依赖元数据读取遇到 JSONDecodeError。该路径未执行代码扫描；错误原文保留在 `pip-install.log`。
- 2026-10-11 正式扫描使用已下载的官方镜像（Semgrep 1.179.0），没有重复下载规则。
- 首次逐文件读取 Windows Docker 只读挂载非常慢，尚未进入分析时停止了仅本次创建的扫描容器。随后 Windows 本机用 `prepare_snapshot.py` 读取当前工作区，生成 ZIP 和逐文件 SHA-256 清单；扫描容器禁网读取此本地 ZIP。
- 官方镜像中的 `python -m semgrep` 返回退出码 2 和弃用提示，未产生扫描结果。执行器改为官方 `semgrep scan` 命令。最终结果以 `semgrep-raw.json` 和 `scan-metadata.json` 为准。
- 初次快照自动遍历时纳入了 `lab/.downloads` 和 `lab/.runtime` 下载的第三方代码。范围检查发现后停止本次扫描；此轮没有用于正式结果。最终快照改用本地 `git ls-files --cached --others --exclude-standard -z` 枚举维护中的文件（含当前修改和未跟踪源码），并继续排除生成物、报告、缓存和运行秘密输入；既不访问远端 GitHub，也不把第三方缓存的告警归入业务源码。
- 只写审计报告、工具准备文件及独立临时快照，不修改业务代码、现有 `.env`、提示词或历史迁移；不读取远端 GitHub，不上传扫描内容、源码和结果。
