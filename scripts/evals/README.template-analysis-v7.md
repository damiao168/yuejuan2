# 答题卡 v7 契约回放

这组工具验证提示词版本、输出契约和错误降级。模型输出来自固定夹具，不会调用真实供应商，也不能据此评价识别准确率、边界质量或线上效果。真实 A/B 分组及人工标注指标见[优化设计](../../docs/architecture/answer-sheet-multimodal-optimization.md)。

## 版本登记

在仓库根目录运行：

```powershell
python scripts/evals/check_template_analysis_prompt_manifest.py
```

默认只检查 [`manifest.v7.json`](../../ai-services/prompts/template-analysis/manifest.v7.json) 是否与实际 system 提示词和参考 schema 一致。变更运行输入后，使用同一命令加 `--write` 正式重建登记，并评审差异。此文件与评分提示词 manifest 分开；参考 schema 固定 `candidate_ids=[]`、`page_no=1`，不能替代逐请求的实际 schema 摘要。

## 固定响应回放

输入 JSON 的顶层必须只有 `schema_version: 1` 和 `cases`。`cases` 包含 1–16 例，文件不超过 16 MiB；每例必须包含以下字段，不接收其他字段：

| 字段 | 含义 |
| --- | --- |
| `case_id` | 1–64 位字母、数字及 `_.:-` 标识，首字符为字母或数字；应使用无个人信息的样本编号 |
| `prompt_variant` | `v6`、`v7-text` 或 `v7-evidence` |
| `request` | `/answer-sheet/analyze` 的请求资料，不能含 `managed_model` 凭据；图片尺寸及 SHA-256 必须真实对应 |
| `model_output` | 固定模型 JSON 响应；v7 观察组须包含内部观察字段 |

在已安装 AI 服务依赖的环境、仓库根目录运行：

```powershell
python scripts/evals/replay_template_analysis_v7.py path/to/controlled-corpus.json --output path/to/safe-report.json
```

输出文件的父目录须已存在。输入夹具若含真实图像或模型原文，应放在项目已有的受控资料目录，不能提交或复制进普通报告；工具不改变输入文件。`case_id` 会进入报告，因此也不能填姓名或考号。

回放保留 OpenCV 几何处理，关闭 OCR、局部填空恢复、静态禁区复核及外部模型。报告只输出候选/待核对数量、固定原因、请求契约 hash 和有界错误码；不会输出框坐标、实际题号、观察字段或图片。即使父进程开启内容诊断和 INFO 日志，也不输出夹具请求 ID 或写原文诊断文件。

进程返回 0 表示已生成回放报告，不表示所有样本通过：需检查每例 `semantic_status`、`error_code` 和目标原因。`completed` 只表示固定响应已完成协议处理；兼容协议的 `status=review_required` 不再要求所有成功结果逐框人工确认。通过自动使用规则的 `supported` 结果直接进入可编辑草稿，实际冲突才需纠错。`supported_count=0` 不是准确率指标，不能以全部降级替代真实模型评测。

## 定向验证

```powershell
python -m pytest -q scripts/evals/test_template_analysis_prompt_manifest.py scripts/evals/test_replay_template_analysis_v7.py
```

AI 服务的流程、观察、诊断和提示词测试位于 `ai-services/tests/test_template_analysis*.py`。从 `ai-services` 目录运行相应测试；测试中的合成页和固定矛盾只验证代码边界，不属于真实模型效果验证。
