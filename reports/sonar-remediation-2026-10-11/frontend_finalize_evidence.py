from pathlib import Path
import json,hashlib,datetime,collections
root=Path('D:/project/yuejuan'); report=root/'reports/sonar-remediation-2026-10-11'
p=report/'frontend-progress.json';j=json.loads(p.read_text(encoding='utf-8'))
reasons={
 'typescript:S8980':'外层 act 合并同一事件批次，用于测试同步 ref 在 disabled 重绘前阻止提交或退出。拆为各自 fireEvent 的 act 会提前刷新 UI，失去该时序保证。',
 'typescript:S6822':'listStyle:none 时 Safari 会从可访问性树移除原生列表；显式 role=list 保留列表语义，已有中文维护注释与 MDN 依据。',
 'javascript:S905':'playwright-cli run-code 接收整个文件中的函数表达式，由工具传入真实 page 并执行；不能改成无人调用的模块导出或自行执行脚本。'
}
evidence=[]
for x in j['issues']:
 if x['status']=='needs_platform_disposition':
  x['reason']=reasons[x['rule']]
  x['source_sha256']=hashlib.sha256((root/x['file']).read_bytes()).hexdigest()
  refs=({'typescript:S8980':['apps/student-portal/src/App.tsx','frontend-types-execution.json'],
         'typescript:S6822':['https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/Properties/list-style#accessibility','apps/web-admin/src/pages/ArbitrationTaskRail.tsx'],
         'javascript:S905':['docs/architecture/DIAGRAM_MAINTENANCE.md','docs/architecture/evidence/acceptance-report.md','scripts/ci/architecture-audit-tools.test.mjs','frontend-scripts-0.log']}[x['rule']])
  x['evidence']=refs;evidence.append(x.copy())
 elif x['status']=='implemented_needs_rescan':x['reason']='已修改当前源码；需串行补验证及统一 SonarQube 复扫确认。'
 elif x['status']=='already_resolved_needs_rescan':x['reason']='工作区基线已使用独立键盘按钮，当前代码不存在原报告的行回调缺陷；本次未改该行为，待平台复扫。'
for file,entry in j['files'].items():
 if (root/file).exists():entry['current_sha256']=hashlib.sha256((root/file).read_bytes()).hexdigest()
j['validations']=[
 {'name':'同一当前工作区输入下旧/新 AST 提取完整 JSON 比较','result':'passed','call_sites':405,'evidence':'frontend-comparison.json'},
 {'name':'Node schema-version 与架构审计工具测试','result':'passed','tests':5,'evidence':'frontend-scripts-0.log'},
 {'name':'desktop-client 四份现有测试','result':'passed','tests':100,'evidence':'frontend-desktop-execution.json'},
 {'name':'student-portal 三份现有测试','result':'passed','tests':48,'evidence':'当前会话先前工具输出，未保存原始日志'},
 {'name':'AnswerEvidencePane/useAnswerViewer 两份现有测试','result':'passed','tests':10,'evidence':'当前会话先前工具输出，未保存原始日志'},
 {'name':'web-admin production-contracts 首次验证','result':'passed_before_additional_test_edits','tests':17,'evidence':'当前会话先前工具输出，未保存原始日志'},
 {'name':'三个应用首轮类型检查','result':'passed_before_additional_test_edits','evidence':'当前会话先前工具输出；新改测试后仍需补跑'},
 {'name':'Biome lint','result':'passed_with_warnings','warnings':15,'evidence':'frontend-lint-execution.json'},
 {'name':'STORY049/052 静态门禁','result':'passed','evidence':'frontend-scripts-execution.json'},
 {'name':'lab 现有测试','result':'passed','tests':194,'evidence':'frontend-scripts-4.log'},
 {'name':'web-admin 38 份测试并行尝试','result':'incomplete_worker_crash','evidence':'frontend-web-execution.json','detail':'ERR_IPC_CHANNEL_CLOSED，不能计为完整通过'},
 {'name':'第二轮类型检查','result':'partial_resource_failure','evidence':'frontend-types-execution.json','detail':'student-portal 成功；desktop/web 原生 OOM 退出 134，待串行重跑'},
 {'name':'production-contracts 第二轮尝试','result':'resource_failure','evidence':'frontend-scripts-execution.json','detail':'Windows 0xC000012D 提交内存不足'},
 {'name':'Playwright 9 份规格 47 项尝试','result':'interrupted_needs_serial_retry','evidence':'frontend-e2e-interrupted.json','detail':'浏览器资源不足后终止自有测试树；另有移动端截图差异必须单独复核'}
]
j['summary']=dict(collections.Counter(x['status'] for x in j['issues']))
p.write_text(json.dumps(j,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
(report/'frontend-platform-disposition-evidence.json').write_text(json.dumps({'count':6,'issues':evidence,'state':'awaiting_platform_review'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
e2elog=report/'frontend-e2e-0.log'
(report/'frontend-e2e-interrupted.json').write_text(json.dumps({'result':'interrupted','recorded_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'started_at':'2026-10-11T09:08:54Z','owned_process_root':42740,'requested_specs':9,'requested_tests':47,'reason':'root 协调资源窗口前，核对命令后只终止自己的 frontend_validate.py e2e 进程和后代','log':e2elog.name,'log_sha256':hashlib.sha256(e2elog.read_bytes()).hexdigest(),'remaining':'串行重跑，先检查 390x844 截图差异；不能计为通过'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
md='''# 前端 SonarQube 整改进度

原始 JS/TS/TSX/MJS 问题 172 项。第一轮源码处理：165 项实现待复扫，1 项基线已有修复待复扫，6 项需平台审定。HTML 内嵌问题由 root 处理。本账本不代表 SonarQube 已关闭问题。

7 项 CRITICAL 已处理：两份 API AST 提取器共享权威扫描器，按调用收集阶段拆解并保留 405 个调用点的完整 JSON；动态路径规范化改为线性扫描；draw.io 几何检查按顶点、边和障碍拆解；迁移和部署检查显式排序。MAJOR 包含真实键盘按钮、独立 status 输出、移除 networkidle/force 点击。MINOR 包含 React/AntD 当前 API、显示值类型边界、可选链、专用断言和单次正则 exec。

已有验证：desktop 100 项，student 48 项，答题证据/查看器 10 项，Node 5 项，lab 194 项；AST 405 个调用点完整等价；STORY049/052 和 Biome 成功（15 个现有警告）。首轮三应用类型检查和 production-contracts 17 项成功；后续新增测试修改后仍需串行补跑。详见 frontend-progress.json 中的 validations 与原始日志。

尚未完成：web 测试工作进程崩溃；第二轮 desktop/web 类型检查 OOM；生产契约第二次尝试 Windows 提交内存不足；Playwright 测试遇到资源不足并终止自己的进程树。移动端 390x844 截图约 6% 差异，可能与 InputNumber 单位迁移有关，需实际图像和串行测试确认，不更改基准图掩盖问题。全部平台复扫由 root 统一执行。

文件审读清单沿用 frontend-progress.json：只有实际完整审读的文件能标完成；仅查看告警上下文的文件保持 pending。

## 六项需要平台审定的语义证据

'''
for x in evidence:
 md+=f"- `{x['key']}` — `{x['rule']}`，`{x['file']}` 原行 {x['original_line']}。{x['reason']} SHA256 `{x['source_sha256']}`。\n"
md+='''
`App.releaseConsistency` 保护图片失败后同一事件批次的直接 submit；两个 `App.submissionReceipt` 保护退出/提交的竞争顺序。每个 fireEvent 已封装 act，但外层 act 故意阻止事件间提前重绘；删掉会失去同步 ref 的覆盖。48 项学生端测试和当前 student 类型检查成功，尚须保存重跑日志供平台复核。

Safari 的列表兼容依据来自 [MDN list-style 可访问性说明](https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/Properties/list-style#accessibility)。当前代码保留 `ul role="list"` 和 `listStyle:"none"`，附近注释直接说明原因。

两个工具文件供 `playwright-cli run-code` 读取。`DIAGRAM_MAINTENANCE.md` 与 `evidence/acceptance-report.md` 记录真实调用和 70 页验证；Node 测试把 render_native.js 的完整函数表达式交给 vm 并传入图形夹具执行，保护顶点/边界/端点/背景和悬空边。这个测试不等同真实浏览器渲染，原报告另有真实 EditorUi 证据。capture_preview_qa.js 的调用形式同样必须保留，未在本次重启浏览器。
'''
(report/'frontend-progress.md').write_text(md,encoding='utf-8')
print(json.dumps(j['summary']))
