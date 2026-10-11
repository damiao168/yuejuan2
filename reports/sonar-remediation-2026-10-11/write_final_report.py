"""从最终本地平台原始状态生成交付报告；不把待复扫或条件跳过写成通过。"""
from pathlib import Path
from collections import Counter, defaultdict
from datetime import datetime
import argparse
import csv
import hashlib
import json

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
ORIGINAL = ROOT / 'reports/platform-audit-2026-10-10/sonar'


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def link(path, label=None):
    target = path.resolve().as_posix()
    if ' ' in target:
        target = '<' + target + '>'
    return f'[{label or path.name}]({target})'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--checkpoint', required=True)
    args = parser.parse_args()
    if Path(args.checkpoint).name != args.checkpoint or not args.checkpoint.startswith('full-checkpoint-'):
        parser.error('checkpoint must be a direct full-checkpoint directory name')
    checkpoint = OUT / args.checkpoint
    audit = read(checkpoint / 'final-audit.json')
    if audit['all_issue_work_complete'] is not True:
        raise RuntimeError('Final current-source issue audit is not complete')
    target = OUT / 'final-remediation-report.md'
    if target.exists():
        raise RuntimeError('Preserve previous final report')
    old = read(ORIGINAL / 'issues.json')['issues']
    old_by_key = {row['key']: row for row in old}
    previous_new = [row for row in read(OUT / 'full-checkpoint-02/issues.json')['issues'] if row['key'] not in old_by_key]
    previous_new_by_key = {row['key']: row for row in previous_new}
    final_original = read(checkpoint / 'final-original-statuses.json')['issues']
    final_new = read(checkpoint / 'final-checkpoint02-new-statuses.json')['issues']
    source_ledger = {row['issue_key']: row for row in read(OUT / 'issues-ledger.json')['issues']}
    new_ledger = {}
    for name in ('root-new-findings-progress.json', 'python-new-findings-progress.json', 'go-new-findings-progress.json'):
        data = read(OUT / name)
        for row in data.get('issues', data.get('issue_states', [])):
            assert row['key'] not in new_ledger
            new_ledger[row['key']] = row
    assert len(new_ledger) == 47
    tracked = [('original', row, old_by_key[row['key']]) for row in final_original]
    tracked.extend(('checkpoint02_new', row, previous_new_by_key[row['key']]) for row in final_new)
    rows = []
    for group, issue, baseline in tracked:
        local = source_ledger.get(issue['key'], new_ledger.get(issue['key'], {}))
        rows.append({'group': group, 'key': issue['key'], 'rule': issue['rule'],
                     'severity': baseline['severity'], 'type': baseline['type'],
                     'file': issue['component'].split(':', 1)[1],
                     'original_line': baseline.get('line'), 'latest_line': issue.get('line'),
                     'platform_status': issue['issueStatus'], 'message': baseline['message'],
                     'source_record_status': local.get('status', local.get('state')),
                     'source_change_or_reason': local.get('change_or_reason', local.get('reason', local.get('change'))),
                     'owner_record': local.get('owner_report'), 'analysis_id': audit['analysis_id']})
    assert len(rows) == len({row['key'] for row in rows}) == 2270
    with (OUT / 'final-issues.csv').open('w', encoding='utf-8-sig', newline='') as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    (OUT / 'final-issues.json').write_text(json.dumps({'analysis_id': audit['analysis_id'], 'count': len(rows), 'issues': rows}, ensure_ascii=False, indent=2), encoding='utf-8')
    old_metrics = {row['metric']: row.get('value') for row in read(ORIGINAL / 'metrics.json')['component']['measures']}
    metrics = {row['metric']: row.get('value') for row in read(checkpoint / 'metrics.json')['component']['measures']}
    languages = defaultdict(Counter)
    rules = defaultdict(Counter)
    for issue in final_original:
        languages[issue['rule'].split(':', 1)[0]][issue['issueStatus']] += 1
        rules[issue['rule']][issue['issueStatus']] += 1
    all_counts = Counter(row['platform_status'] for row in rows)
    now = datetime.now().astimezone().isoformat()
    original_sha = hashlib.sha256((ORIGINAL / 'issues.json').read_bytes()).hexdigest()
    assert original_sha == read(OUT / 'baseline.json')['original_issues_sha256']
    text = [
        '# SonarQube 本地工作区修复与最终复扫报告', '',
        f'交付生成时间：{now}。本次仅处理和扫描 `D:/project/yuejuan` 本地工作区；最终分析 `{audit["analysis_id"]}`，项目 `{audit["project"]}`。没有扫描远端 GitHub，也没有向云端上传、提交或推送代码。', '',
        f'原始 2223 项全部获得明确平台状态：{audit["original_status_counts"].get("FIXED", 0)} 项由源码修复后分析器标为 FIXED，{audit["original_status_counts"].get("FALSE_POSITIVE", 0)} 项经逐项证据审查标为 FALSE_POSITIVE，{audit["original_status_counts"].get("ACCEPTED", 0)} 项保留明确的本机预览 HTTP 例外。', '',
        f'复扫新增 47 项也全部处理：{audit["checkpoint02_new_status_counts"].get("FIXED", 0)} 项 FIXED，{audit["checkpoint02_new_status_counts"].get("FALSE_POSITIVE", 0)} 项 FALSE_POSITIVE。合计跟踪 2270 个唯一问题，当前未处置告警 {audit["active_count"]} 项，安全热点 {metrics.get("security_hotspots")} 项。人工误报及接受事项没有计入源码修复数量。', '',
        '**代码告警已全部处理，但质量门禁仍需按下面的实际状态解释；静态告警归零不能代表业务评测、覆盖率和集成测试全部达标。**', '',
        '## 1. 扫描范围与证据完整性', '',
        f'- 最终 Scanner 实际退出 0；本地 Compute Engine 为 SUCCESS；导出完整。最终检查使用 {args.checkpoint}，之前失败及中间检查点全部保留。',
        '- 使用官方 SonarQube Community Build 26.9.0.129388 与 SonarScanner CLI 8.1.0.6389，沿用原本机平台和独立私有审计项目。',
        f'- 冻结维护文件 {audit["maintained_files"]} 个，原问题涉及的 {audit["original_issue_files_included"]} 个文件全部包含；Sonar 生产文件度量为 {metrics.get("files")} 个、生产代码 {metrics.get("ncloc")} 行。测试、配置、支持文件和生产度量的口径不同。',
        '- 当前维护文件及类型解析支持文件与最终冻结输入逐字节一致；原排除策略、质量配置、活跃规则数量和规则更新时间均保持一致。没有用排除文件、禁用规则或降低门槛消除告警。',
        '- 原始 issues.json 的 SHA256 与接手基线相同。原有未提交修改保留；额外已有改动按当前哈希复核，没有回滚其它工作。',
        '- 自动文件清单不表示完整人工审读。各分工按实际审读范围记录；只读过局部或尚未审读的文件仍为 pending。',
        '- 维护清单中的 SQL、Shell、PowerShell 和 YAML 等不表示获得与 Go/Python/TypeScript 相同的专用质量分析深度。依赖分析在本轮日志明确为 skipped，没有把这轮 Sonar 扫描当作完整依赖漏洞审计。',
        f'- 原始问题及新增问题均逐 key 对账，规则、项目和文件身份完整一致。详见 {link(checkpoint / "final-audit.json", "最终平台与源码审计")}、{link(checkpoint / "source-scope.json", "冻结源码 SHA 清单")}。', '',
        '另一智能体独立核对全部 2270 个问题、2654 份维护源码和 2 份类型支持文件，检查无缺漏、无身份或严重度漂移，35 项人工处置理由及哈希均吻合；详见 ' + link(OUT / 'delivery-independent-checkpoint04-final-platform-audit-python-agent.md', '最终独立审计') + '。', '',
        '## 2. 原始问题按语言的最终结果', '',
        '| 规则语言 | 原始数 | FIXED | FALSE_POSITIVE | ACCEPTED | OPEN |',
        '| --- | ---: | ---: | ---: | ---: | ---: |',
    ]
    for name, counts in sorted(languages.items()):
        text.append(f'| {name} | {sum(counts.values())} | {counts["FIXED"]} | {counts["FALSE_POSITIVE"]} | {counts["ACCEPTED"]} | {counts["OPEN"]} |')
    text.extend(['', '原始分类为 Bug 1653、代码异味 560、安全漏洞候选 10；严重级别为 CRITICAL 230、MAJOR 1829、MINOR 164。这些是平台候选分类，并不表示原来存在相同数量的生产漏洞。', '',
                 '主要改动包括生成模板和静态页面的通用字体后备、页面可访问名称及外部资源完整性；Go/Python 复杂流程和测试的职责拆分；测试异常输入的求值边界；TypeScript 异步回调及类型表达；SDK 文件操作的竞争、关闭责任和重试边界。生成文件通过权威模板或既有流程更新，历史 SQL 迁移和锁文件没有直接补写注释。', '',
                 '完整逐项结果见 ' + link(OUT / 'final-issues.csv', '2270 项最终 CSV') + ' 和 ' + link(OUT / 'final-issues.json', '2270 项最终 JSON') + '。逐项记录保留原行号、当前行号、原分类、规则、平台状态和源码处理说明。', '',
                 '## 3. 人工误报与明确接受的例外', '',
                 '- 原始 30 项误报：15 条把中文维护说明识别成注释掉的代码、6 条将固定种子的合成绘图噪声当成安全随机数、1 条测试比较顺序、3 条有意的 React act 外层批处理、1 条兼容 Safari 的列表角色、2 条 Playwright 工具函数输入、2 条已在 serve 前完成 TLS 包装的监听入口。',
                 '- 新增 4 项误报：2 条 page.evaluate 浏览器执行单元中的几何函数作用域、1 条字体后备中文维护说明、1 条 playwright-cli run-code 接收的完整 async 函数输入。实际安装的工具 loader 已只读核对，不根据函数外形推测用途。',
                 '- 唯一 ACCEPTED 是固定 `127.0.0.1:8766` 的本地图纸预览 HTTP 服务。已有路径范围、解析后路径边界及请求体限制保留；这不等于改成 HTTPS，也不作为生产 HTTP 部署许可。',
                 '- TLS 两项仍允许显式开发环境、真实 bool 授权及字面 IPv4 回环的 HTTP 例外；生产、缺省及未知环境在 bind 前要求成对证书，最低 TLS1.2，证书包装在 serve 前完成。真实握手和负例证据支持误报处置，但不声称所有环境都走 HTTPS。',
                 '- 每次处置都检查当前源码 SHA 与对应冻结扫描 SHA，相同后在本地平台附依据、转换状态并 GET 回查；没有用人工 resolve 替代源码 FIXED。',
                 '', '处置证据：' + '、'.join(link(OUT / name) for name in ('root-platform-disposition-results.json', 'root-platform-disposition-phase02-results.json', 'root-platform-disposition-phase03-results.json', 'full03-fp-independent-review-python-agent.md')) + '。', '',
                 '## 4. 实际验证、跳过与失败记录', '',
                 '以下列出对本次改动有直接意义的成功批次；批次间存在重复测试及子测试，不相加成一个虚假的全仓测试总数。', '',
                 '| 验证 | 实际结果 | 边界与证据 |',
                 '| --- | --- | --- |',
                 '| Web Admin 38 个测试文件 | 416 pass、0 skip、退出 0 | 单 worker 重试成功，前后源码 SHA 一致；' + link(OUT / 'frontend-web-retry-01-execution.json') + ' |',
                 '| Web Admin 类型检查 | 退出 0、无诊断 | 1280 MiB 堆重试成功；此前真实 OOM 退出 134 保留；' + link(OUT / 'frontend-web-current-typecheck-02.json') + ' |',
                 '| 前端生产契约及构建测量脚本 | 17 pass、0 skip、退出 0 | 没有据此宣称完成浏览器或生产 bundle 验收；' + link(OUT / 'frontend-production-contracts-current-01.json') + ' |',
                 '| 六个最新 Go 关联包 | 1158 Test/subtest pass、2 条件 skip、退出 0 | 真实本地 AI/共享客户端契约的环境条件保留；' + link(OUT / 'go-final-validation-audit.md') + ' |',
                 '| server 包 | 188 Test/subtest pass、62 条件 skip、退出 0 | 61 条 PostgreSQL 环境跳过、1 条 crash-helper 辅助入口跳过；' + link(OUT / 'go-server-batch04-tests.json') + ' |',
                 '| 题库五个 PostgreSQL 场景 | 编译及所属包回归通过；PG 0 pass、5 skip | 缺真实隔离数据库配置，不算题库业务集成通过；' + link(OUT / 'go-server-question-bank-progress.md') + ' |',
                 '| 模型治理重构 | baseline/current 各 366 Test/subtest pass、0 skip | 真实 117 编译输入未变化，逐测试身份及 TRACE 顺序核对；' + link(OUT / 'delivery-independent-fresh-followup-audit-python-agent.md') + ' |',
                 '| 最新 Python 最小补验证 | 43+2 pass、0 skip，两次退出 0 | 变化的 OCR 批次/符号验证/主观题 API 测试按真实依赖 SHA 绑定；' + link(OUT / 'fresh-python-dynamic-01/execution.json') + ' |',
                 '| SDK 文件竞争与生成契约 | 18 pass、0 skip、退出 0 | 短写、截断、竞争重试、权限错误及 FD 关闭责任；' + link(OUT / 'root-new-js-tests-02.json') + ' |',
                 '| 前端 API 扫描工具的固定输入对比 | 输出 1176650 字节完全一致 | 485 TS 源、405 调用、536 后端路由等清单一致；生成写入在内存截获；' + link(OUT / 'frontend-api-scope-equivalence-02.json') + ' |',
                 '| TLS/传输与 XML 加固关联检查 | 已执行真实握手负例及恶意 XML/无网络测试 | 独立 SHA 绑定和输出等价检查；' + link(OUT / 'python-tls-platform-evidence.md') + ' |',
                 '', '最后两个 Go 复杂度项使用单独的接手备份、baseline/current 包测试和完整业务证据比较；结果及逐项保护边界见 ' + link(OUT / 'root-go-final-02-progress.md') + '。另一个智能体全文读审并反向内联新 helper，整文件 token 与 checkpoint03 一致，详见 ' + link(OUT / 'go-import-final-independent-review.md') + '；可靠性重构的全文审查见 ' + link(OUT / 'go-server-reliability-independent-review.md') + '。', '',
                 '历史编译失败、工具真实执行失败、OOM、IPC 故障及重试日志全部保留，没有改写成成功。早期部分 Go 测试没有单独记录测试时 SHA，最终审计明确保留这一证据局限。', '',
                 '**浏览器 E2E 没有全部通过。** 原请求 47 用例的运行在 23 个启动标记后中断，没有最终成功摘要。记录的 10 个失败包括 2 个原有 UI/快照不一致、2 个有资源不足证据的断言超时、6 个浏览器启动/worker/导航失败。预扫描本地基线证明两项 UI 差异已存在；未更新快照或删断言，仍需要健康环境重跑及产品验收。详细证据见 ' + link(OUT / 'frontend-e2e-independent-review-python-agent.md') + '。', '',
                 '本批没有实际运行图纸字号浏览器 QA，也没有构建或拉取 Docker 镜像；相关小改动通过源码、语法和最终规则复扫验证，不能改写为对应运行验收已经完成。', '',
                 '## 5. 最终平台度量与质量门禁', '',
                 '| 度量 | 原始扫描 | 最终扫描 |', '| --- | ---: | ---: |'])
    for name in ('ncloc', 'bugs', 'vulnerabilities', 'code_smells', 'security_hotspots', 'duplicated_lines_density', 'sqale_index', 'sqale_rating', 'reliability_rating', 'security_rating'):
        text.append(f'| {name} | {old_metrics.get(name, "未提供")} | {metrics.get(name, "未提供")} |')
    gate = audit['quality_gate']
    text.extend(['', '评级数值 1=A、2=B、3=C、4=D、5=E；sqale_index 为平台估算维护成本，单位分钟，不是已耗工时。', '',
                 f'**质量门禁实际状态为 {gate["status"]}。** 条件逐项列出：', '',
                 '| 条件 | 实际值 | 失败条件 | 状态 |', '| --- | ---: | --- | --- |'])
    for condition in gate.get('conditions', []):
        text.append(f'| {condition["metricKey"]} | {condition.get("actualValue", "未提供")} | {condition["comparator"]} {condition["errorThreshold"]} | {condition["status"]} |')
    text.extend(['', '本次没有导入测试覆盖率，平台 new_coverage/coverage 为 0 是缺少导入数据，不能解释为实测覆盖率 0%。没有放宽 80% 覆盖率门槛或改写质量门禁。本次门禁仅新增覆盖率项失败，新增问题数及新增重复率均满足门槛；交付结论是“代码告警全部处理、覆盖率门禁仍未满足”。', '',
                 'Rust 语言传感器保留原范围，但本次出现 No Cargo manifest found / skipping Clippy analysis，因此不能声称本轮完成 Rust Clippy 构建检查。既有 CodeQL Rust 完整查询属于另一个有独立哈希的历史扫描，不能替代当前工作区的 Clippy 或业务测试。', '',
                 '## 6. 其它平台与业务评测的范围', '',
                 '本轮最终当前工作区复扫是 SonarQube。最初按用户要求完成的 Semgrep、CodeQL、本地 Actions 检查和覆盖率附件保持原样；它们对应各自历史冻结版本，后续源码改变后没有在本轮重新扫描，所以不把历史安全结果当作当前全仓复检结果。', '',
                 '历史完整入口：' + link(ROOT / 'reports/platform-audit-2026-10-10/README.md', '全部平台原始详细报告') + '。Actions 云端 run 和 Codecov 云端上传均没有执行，遵守仅本地要求；最初六包的 49.472% 覆盖率及 releasegate 41.315% 门槛失败也没有被本轮覆盖掉。', '',
                 'SonarQube 告警归零仍不能证明学校间授权隔离、成绩事务并发、裁切/识别准确率、主观题一致性或三智能体仲裁正确。数据库、真实 AI 服务、压力测试和专门业务评测须使用对应环境与数据集独立验证。', '',
                 '## 7. 原始规则逐项统计', '',
                 '| 规则 | 原始数 | FIXED | FALSE_POSITIVE | ACCEPTED | OPEN |', '| --- | ---: | ---: | ---: | ---: | ---: |'])
    for name, counts in sorted(rules.items(), key=lambda item: (-sum(item[1].values()), item[0])):
        text.append(f'| {name} | {sum(counts.values())} | {counts["FIXED"]} | {counts["FALSE_POSITIVE"]} | {counts["ACCEPTED"]} | {counts["OPEN"]} |')
    text.extend(['', '最终报告以真实平台状态为准；分工账本中的待复扫文字保留历史来源，其最新平台状态及分析 ID 另存于总账，不把历史证据时间改成最终扫描时间。', ''])
    target.write_text('\n'.join(text), encoding='utf-8')
    data = {'generated_at': now, 'checkpoint': args.checkpoint, 'analysis_id': audit['analysis_id'],
            'original_status_counts': audit['original_status_counts'], 'checkpoint02_new_status_counts': audit['checkpoint02_new_status_counts'],
            'all_tracked_status_counts': dict(all_counts), 'tracked_count': len(rows), 'active_count': audit['active_count'],
            'quality_gate': gate, 'coverage_imported': False, 'all_issue_work_complete': True,
            'original_issues_sha256_unchanged': original_sha, 'old_metrics': old_metrics, 'final_metrics': metrics}
    (OUT / 'final-report-data.json').write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'report': str(target), 'tracked': len(rows), 'status_counts': dict(all_counts), 'quality_gate': gate['status']}, ensure_ascii=False))


if __name__ == '__main__':
    main()
