from pathlib import Path
import hashlib,json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
path=report/'frontend-progress.json';ledger=json.loads(path.read_text(encoding='utf-8'))
tests=json.loads((report/'frontend-web-retry-01-execution.json').read_text(encoding='utf-8'))
types=json.loads((report/'frontend-web-current-typecheck-02.json').read_text(encoding='utf-8'))
assert tests['exit_code']==0 and not tests['changed_sources_during_tests'] and types['exit_code']==0 and not types['changed_sources']
file='apps/web-admin/src/components/ResponsiveTable.tsx';sha=hashlib.sha256((root/file).read_bytes()).hexdigest()
assert tests['source_sha_before'][file]==sha and types['source_sha_before'][file]==sha
for issue in ledger['issues']:
 if issue['key']=='ebbf07bc-b59d-4c1d-9f6f-c323abc27810':
  issue['validation']={'current_sha256':sha,'typecheck':'frontend-web-current-typecheck-02.json','tests':'frontend-web-retry-01-execution.json','responsive_table_tests':10,'platform_rescan':'pending root checkpoint03'}
for name,result,evidence,extra in [
 ('web-admin 最新串行 38 份现有测试','passed','frontend-web-retry-01-execution.json',{'tests':416,'files':38,'skips':0,'source_sha_stable':True}),
 ('web-admin 最新类型检查','passed','frontend-web-current-typecheck-02.json',{'source_sha_stable':True,'prior_failure_preserved':'frontend-web-current-typecheck-01.json'}),
 ('Playwright 失败只读独立归因','reviewed_with_open_acceptance_failures','frontend-e2e-independent-review-python-agent.json',{'detail':'390x844 布局/文案与原工作区一致，旧快照仍验收失败；移动壳/确认页 trace 资源错误待复验。'})]:
 ledger['validations']=[item for item in ledger['validations'] if item['name']!=name]
 ledger['validations'].append({'name':name,'result':result,'evidence':evidence,**extra})
path.write_text(json.dumps(ledger,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
md=(report/'frontend-progress.md').read_text(encoding='utf-8');tail=md[md.index('## 六项需要平台审定'):]
head='''# 前端 SonarQube 整改进度

原始 JS/TS/TSX/MJS 172 项：166 项源码已实现待平台复扫，6 项保留必要语义待平台审定。HTML 内嵌问题由 root 处理。本账本不代表 SonarQube 已关闭问题。

7 项 CRITICAL 包括共享 API AST 扫描器、线性动态路径扫描、draw.io 几何阶段拆解及确定性排序；保留 405 个调用点完整 JSON 等价。其余修改覆盖键盘交互、React/AntD API、显示值类型边界、可选链、专用断言和单次正则匹配。ResponsiveTable 原 S6544 已按真实 Array.some 回调纠正为显式 Boolean，未改变原真假值条件。

最新 web-admin 串行 38 份测试 / 416 项全部通过、0 跳过，类型检查退出 0，测试及类型检查源码 SHA 均稳定，包含 ResponsiveTable 的 10 项现有测试。之前的 IPC、OOM 和 Windows 提交内存不足日志完整保留。其他已完成验证为 desktop 100、student 48、答题证据/查看器 10、Node 5、lab 194；AST 405 调用完整等价；STORY049/052 与 Biome 成功（15 个已有警告）。前端证据在 frontend-progress.json，各验证只覆盖相应冻结源码。

Playwright 的资源错误仍需条件允许时复验。独立只读图像及源码比对确认 390x844 的布局/文案差异早于本轮 Sonar 修复，原工作区已有相同移动布局；旧快照目前仍是待验收失败，没有更新基准图。详细归因见 frontend-e2e-independent-review-python-agent.json/md。本轮未将截图差异计为通过。

逐文件状态沿用 frontend-progress.json：完整审读才标完成，仅告警及调用上下文审读的文件保持 pending。统一平台复扫归 root 负责。

'''
(report/'frontend-progress.md').write_text(head+tail,encoding='utf-8')
follow=report/'frontend-checkpoint02-followup.md'
text=follow.read_text(encoding='utf-8')
text+='\n最新验证：root 的 web-admin 38 份串行测试 / 416 项通过（含 ResponsiveTable 10 项）、web-admin 类型检查退出 0，两个报告中该文件 SHA 与当前字节一致。证据 frontend-web-retry-01-execution.json、frontend-web-current-typecheck-02.json。平台状态仍待 checkpoint03；旧内存失败保留。\n'
follow.write_text(text,encoding='utf-8')
print(json.dumps({'responsive_table_sha256':sha,'tests':416,'typecheck_exit':0}))
