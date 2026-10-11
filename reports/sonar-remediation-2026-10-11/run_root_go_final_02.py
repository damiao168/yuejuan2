from collections import Counter,defaultdict
from pathlib import Path
import datetime as dt
import hashlib,json,os,re,subprocess,tempfile,time

ROOT=Path('D:/project/yuejuan')
OUT=ROOT/'reports/sonar-remediation-2026-10-11'
REL='services/api-gateway/tools/import-fujian-2024-math-simulation/main_batch9_test.go'
FILE=ROOT/REL
BACKUP=Path('D:/tools/sonar-remediation-20261011/root-go-final-02-baseline')/REL
PREFIX='root-go-final-02-'
GO='D:/tools/go/bin/go.exe'
ENV={**os.environ,'GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'1','GOGC':'50'}

def write(name,value):
    path=OUT/(PREFIX+name)
    assert not path.exists(),'preserve prior evidence: '+str(path)
    path.write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')

def source_hashes():
    module=ROOT/'services/api-gateway'
    paths=sorted(list(module.rglob('*.go'))+[module/'go.mod',module/'go.sum'])
    return {path.relative_to(ROOT).as_posix():hashlib.sha256(path.read_bytes()).hexdigest() for path in paths if path.is_file()}

def format_source():
    command=['D:/tools/go/bin/gofmt.exe',str(FILE)]
    result=subprocess.run(command,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=ENV)
    write('format-execution.json',{'command':command,'exit_code':result.returncode,'stderr':result.stderr.decode('utf-8',errors='replace')})
    assert result.returncode==0
    if result.stdout!=FILE.read_bytes():
        with tempfile.NamedTemporaryFile(dir=FILE.parent,delete=False) as handle:
            handle.write(result.stdout);temporary=Path(handle.name)
        os.replace(temporary,FILE)

def structure():
    command=[GO,'run',str(OUT/'go-structure-check.go')]
    result=subprocess.run(command,input=json.dumps([REL]).encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE,cwd=ROOT,env=ENV)
    write('structure-execution.json',{'command':command,'exit_code':result.returncode,'stderr':result.stderr.decode('utf-8',errors='replace'),'limit':'本地辅助估值，不替代安装版本的 SonarQube 复扫'})
    assert result.returncode==0
    summaries=json.loads(result.stdout)
    write('structure.json',summaries)
    return summaries

def run(label,overlay=None):
    command=[GO,'test','-json','-p','1','-count=1','-timeout=5m']
    if overlay:command.append('-overlay='+str(overlay))
    command.append('./tools/import-fujian-2024-math-simulation')
    before=source_hashes();start=dt.datetime.now().astimezone().isoformat();clock=time.monotonic()
    log=OUT/(PREFIX+label+'-events.jsonl')
    assert not log.exists(),'preserve prior log'
    with log.open('wb') as stream:
        code=subprocess.run(command,cwd=ROOT/'services/api-gateway',env=ENV,stdout=stream,stderr=subprocess.STDOUT).returncode
    after=source_hashes();finish=dt.datetime.now().astimezone().isoformat()
    execution={'command':command,'started_at':start,'completed_at':finish,'exit_code':code,'duration_seconds':time.monotonic()-clock,'source_sha_before':before,'source_sha_after':after,'changed_sources':[file for file in before if before[file]!=after.get(file)],'GOMAXPROCS':1,'GOGC':50,'network_dependency_fetch_disabled':True,'database_environment_cleared':False,'baseline_overlay':str(overlay) if overlay else None,'baseline_overlay_sha256':hashlib.sha256(BACKUP.read_bytes()).hexdigest() if overlay else None}
    write(label+'-execution.json',execution)
    events=[];non_json=[]
    for line in log.read_text(encoding='utf-8',errors='replace').splitlines():
        try:events.append(json.loads(line))
        except json.JSONDecodeError:non_json.append(line)
    tests=[event for event in events if event.get('Test') and event['Action'] in {'pass','fail','skip'}]
    buffers=defaultdict(str);proofs=[]
    for event in events:
        key=(event.get('Package'),event.get('Test'))
        buffers[key]+=event.get('Output','')
        while '\n' in buffers[key]:
            line,_,buffers[key]=buffers[key].partition('\n')
            if 'FUJIAN_IMPORT_BATCH9_PROOF ' in line:
                proofs.append(json.loads(line.split('FUJIAN_IMPORT_BATCH9_PROOF ',1)[1]))
    result={'exit_code':code,'test_counts':dict(Counter(event['Action'] for event in tests)),'all_test_results':tests,'business_proofs':proofs,'non_json_output':non_json,'source_sha_unchanged':not execution['changed_sources'],'event_log':log.relative_to(OUT).as_posix()}
    write(label+'-result.json',result)
    print(json.dumps({'label':label,'exit_code':code,'counts':result['test_counts'],'proofs':len(proofs),'source_sha_unchanged':result['source_sha_unchanged']}),flush=True)
    assert code==0 and not execution['changed_sources'],label
    return result

def literals(source):
    pattern=r'//[^\n]*|/\*[\s\S]*?\*/|"(?:\\.|[^"\\])*"|`[^`]*`|\x27(?:\\.|[^\x27\\])*\x27'
    return Counter(match.group() for match in re.finditer(pattern,source) if not match.group().startswith(('//','/*')))

format_source()
summaries=structure()
before_source=BACKUP.read_text(encoding='utf-8');after_source=FILE.read_text(encoding='utf-8')
static={'all_string_rune_raw_literals_retained':literals(before_source)==literals(after_source),'literal_removed':list((literals(before_source)-literals(after_source)).elements()),'literal_added':list((literals(after_source)-literals(before_source)).elements()),'guard_counts_before':len(re.findall(r'\bt\.(Fatal|Fatalf|Error|Errorf)\(',before_source)),'guard_counts_after':len(re.findall(r'\bt\.(Fatal|Fatalf|Error|Errorf)\(',after_source)),'subtest_calls_before':before_source.count('t.Run('),'subtest_calls_after':after_source.count('t.Run('),'comment_standard_read':'docs/engineering/commenting-standard.md','original_baseline_read':'D:/tools/sonar-remediation-20261011/baseline/'+REL,'checkpoint03_owner_changes_preserved':True,'review_status':'annotated','review_scope':'完整审读当前与原始文件，包括配置恢复、模板几何、HTTP表单、SQL驱动、注册事务、故障导入及裁剪修复；本次只提取上传协议校验和故障输入准备。'}
assert static['all_string_rune_raw_literals_retained'] and static['guard_counts_before']==static['guard_counts_after'] and static['subtest_calls_before']==static['subtest_calls_after']
write('static-review.json',static)
overlay=OUT/(PREFIX+'baseline-overlay.json')
assert not overlay.exists()
overlay.write_text(json.dumps({'Replace':{str(FILE):str(BACKUP)}}),encoding='utf-8')
baseline=run('baseline',overlay)
current=run('current')
assert baseline['test_counts']==current['test_counts']
assert [(x['Test'],x['Action']) for x in baseline['all_test_results']]==[(x['Test'],x['Action']) for x in current['all_test_results']]
assert baseline['business_proofs']==current['business_proofs'] and current['business_proofs']
equivalence={'same_test_counts':True,'same_test_identities_and_results':True,'same_complete_business_proofs':True,'proof_count':len(current['business_proofs']),'test_counts':current['test_counts'],'baseline_overlay_only':True,'original_diagnostics_and_business_inputs_retained':static['all_string_rune_raw_literals_retained'],'source_sha256':hashlib.sha256(FILE.read_bytes()).hexdigest(),'evidence':[PREFIX+'baseline-result.json',PREFIX+'current-result.json',PREFIX+'static-review.json'],'sonar_confirmation':'pending root checkpoint04'}
write('equivalence.json',equivalence)
by_name={item['name']:item for item in summaries}
selected=['TestBatch9UploadProtocol','assertBatch9UploadRequest','TestBatch9ImportStageOrderAndFailureResult','prepareBatch9ImportFailureDataset']
assert all(by_name[name]['cognitive_estimate']<=15 for name in selected)
keys={'6eb1de9a-c6f8-4662-9a79-01e20787127a':('TestBatch9UploadProtocol',21,'assertBatch9UploadRequest'),'32ec19c6-7711-4694-b5dd-6577c7ecf0e9':('TestBatch9ImportStageOrderAndFailureResult',19,'prepareBatch9ImportFailureDataset')}
issues=json.loads((OUT/'full-checkpoint-03/issues.json').read_text(encoding='utf-8'))['issues']
states=[]
for issue in issues:
    if issue['key'] not in keys:continue
    name,complexity,helper=keys[issue['key']]
    states.append({'key':issue['key'],'rule':issue['rule'],'severity':issue['severity'],'path':REL,'checkpoint03_line':issue['line'],'checkpoint03_cognitive_complexity':complexity,'checkpoint03_flows':issue['flows'],'function':name,'named_helper':helper,'current_structure_estimate':by_name[name]['cognitive_estimate'],'helper_structure_estimate':by_name[helper]['cognitive_estimate'],'state':'validated_source_fix_pending_platform_rescan','current_sha256':equivalence['source_sha256'],'evidence':[PREFIX+'equivalence.json',PREFIX+'structure.json',PREFIX+'current-execution.json']})
assert len(states)==2
write('progress.json',{'original_issue_count':2,'issue_states':states,'files':{REL:static},'validation':equivalence,'source_frozen':True,'no_rule_or_scan_scope_change':True})
md=f'''# 导入工具最后两项 Go 复杂度修复

full-checkpoint-03 仍 OPEN 的 2 项 CRITICAL 已完成源码修复。上传协议测试提取完整请求认证/入口及 multipart 表单校验，原 calls++ 仍先执行；导入失败测试提取故障输入夹具，区域删除与题序交换仍发生在 HTTP/SQL 事件记录前。保留 root 在 checkpoint03 前的全部修改。

原平台复杂度分别 21 和 19。原上传两个 if 嵌套各计 +4，导入故障输入两个 if 嵌套各计 +3；提取为独立具名 helper 后，辅助 AST 估值分别 {by_name[selected[0]]['cognitive_estimate']} 和 {by_name[selected[2]]['cognitive_estimate']}，新 helper 分别 {by_name[selected[1]]['cognitive_estimate']} 和 {by_name[selected[3]]['cognitive_estimate']}。辅助估值不能代替实际 SonarQube 复扫，原 key 仍待 checkpoint04 确认。

修复前 checkpoint03 通过 Go overlay 重跑同一个 tools 包，修复后运行当前工作区版本，两次均实际退出 0：{current['test_counts'].get('pass',0)} 条 Test/subtest 通过、{current['test_counts'].get('skip',0)} 跳过。逐 Test 身份与结果一致，{len(current['business_proofs'])} 份完整结构化业务 JSON 证据完全一致，覆盖 HTTP/SQL 调用、事务/回滚及故障结果。测试前后整个 api-gateway 的 Go 源码及 go.mod/go.sum SHA 稳定，原全部字符串/诊断、Fatal/Error 数量、t.Run 数量保留。

当前源码已冻结，SHA256 `{equivalence['source_sha256']}`。新基线单独保存在 root-go-final-02-baseline；未覆盖历史基线，未修改其它源码或平台配置。详见 root-go-final-02-progress.json、root-go-final-02-equivalence.json 及两份 execution/events 原始日志。
'''
(OUT/(PREFIX+'progress.md')).write_text(md,encoding='utf-8')
print(json.dumps({'source_frozen':True,'issue_count':2,'sha256':equivalence['source_sha256'],'counts':current['test_counts'],'proof_count':len(current['business_proofs']),'complexity_estimates':{name:by_name[name]['cognitive_estimate'] for name in selected}}),flush=True)
