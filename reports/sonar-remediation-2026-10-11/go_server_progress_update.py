from pathlib import Path
import json,collections,hashlib,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
path=report/'go-server-progress.json';ledger=json.loads(path.read_text(encoding='utf-8'))
done={'appeal_route_test.go':[83],'e2e_business_command_test.go':[117,372,507],'e2e_story056_test.go':[621],
 'score_route_test.go':[18],'readers_batch47_test.go':[65],'platform_schools_route_test.go':[18],'server_test.go':[443],
 'e2e_math_rubric_score_test.go':[27],'e2e_math_effective_evidence_test.go':[17],'e2e_idempotency_test.go':[16],
 'e2e_scoring_command_test.go':[18],'e2e_story060_provision_test.go':[12],'migration_upgrade_test.go':[21],
 'managed_config_postgres_test.go':[15],'e2e_mfa_test.go':[25,231],
 'e2e_story056_calibration_test.go':[21],'e2e_story060_capture_recovery_test.go':[20],
 'e2e_anonymous_paper_test.go':[20],'e2e_core_workflow_test.go':[59],'e2e_postgres_test.go':[319,791],
 'e2e_math_grading_binding_test.go':[19],'e2e_story060_roster_test.go':[174],
 'review_route_test.go':[183,465,821,987],'e2e_story056_lease_test.go':[195],
 'e2e_exam_management_test.go':[18],'e2e_auth_security_test.go':[20],
 'e2e_subjective_command_test.go':[14],'e2e_math_verification_runtime_test.go':[21],
 'e2e_scoring_double_mark_test.go':[15],'e2e_story061_governance_test.go':[16]}
done['e2e_story056_test.go'].append(45)
done['e2e_postgres_test.go'].append(34)
done['e2e_review_recovery_test.go']=[38]
done['e2e_review_repairs_test.go']=[126,288]
done['e2e_exam_archive_commands_test.go']=[21]
done['e2e_story060_sheet_identity_test.go']=[17,403]
done['e2e_subjective_panel_test.go']=[20]
done['e2e_worker_runtime_reliability_test.go']=[19]
done['e2e_paper_import_reconciliation_test.go']=[22]
done['e2e_reliability_command_test.go']=[41,1050]
question_bank=json.loads((report/'go-server-question-bank-progress.json').read_text(encoding='utf-8'))
delegated_keys={x['key'] for x in question_bank['issue_states']}
fully_read={'score_route_test.go','readers_batch47_test.go','platform_schools_route_test.go','e2e_math_rubric_score_test.go',
 'e2e_math_effective_evidence_test.go','e2e_idempotency_test.go','e2e_scoring_command_test.go',
 'e2e_story060_provision_test.go','migration_upgrade_test.go','managed_config_postgres_test.go',
 'e2e_story060_capture_recovery_test.go','e2e_anonymous_paper_test.go','e2e_math_grading_binding_test.go',
 'e2e_exam_management_test.go','e2e_auth_security_test.go','e2e_subjective_command_test.go',
 'e2e_math_verification_runtime_test.go','e2e_scoring_double_mark_test.go','e2e_story061_governance_test.go',
 'e2e_review_recovery_test.go','e2e_exam_archive_commands_test.go','e2e_story060_sheet_identity_test.go'}
fully_read.update({'e2e_subjective_panel_test.go','e2e_worker_runtime_reliability_test.go','e2e_paper_import_reconciliation_test.go','e2e_reliability_command_test.go'})
for x in ledger['issues']:
 if x['rule']=='go:S107' or x['original_line'] in done.get(Path(x['path']).name,[]):
  x.update(state='implemented_needs_validation',batch='domain_parameters' if x['rule']=='go:S107' else 'named_business_stages',change='按具名业务阶段提取，保留原 guard、Fatal 中止、租约身份、事务/故障恢复和请求顺序',current_sha256=hashlib.sha256((root/x['path']).read_bytes()).hexdigest())
 if x['key'] in delegated_keys:
  x.update(state='delegated_implemented_needs_validation',owner='sonar_go_fix',evidence='go-server-question-bank-progress.json',current_sha256=hashlib.sha256((root/x['path']).read_bytes()).hexdigest())
for file in sorted(set(x['path'] for x in ledger['issues'])):
 ledger['files'][file]={'review_status':'annotated' if Path(file).name in fully_read else 'pending','review_scope':'已完整审读并核对本次维护边界' if Path(file).name in fully_read else '仅原告警函数或参数调用上下文；全文件审读未完成','current_sha256':hashlib.sha256((root/file).read_bytes()).hexdigest()}
ledger['summary']=dict(collections.Counter(x['state'] for x in ledger['issues']))
ledger['source_changed_original_issues']=sum(x['state'] in {'implemented_needs_validation','delegated_implemented_needs_validation'} for x in ledger['issues'])
ledger['validations']=[{'kind':'gofmt','status':'passed','evidence':'go-server-format-atomic-execution.json'},
 {'kind':'Go AST parse','status':'passed_for_current_sources','evidence':'go-server-structure-current.json','execution':'go-server-structure-execution.json','limit':'只有语法/估算；不证明类型、行为或 SonarQube 消警'},
 {'kind':'Go compilation and existing tests','status':'passed_for_frozen_batch04','evidence':'go-server-batch04-tests.json','source_sha':'go-server-batch04-source-sha.json','pass':188,'skip':62,'limit':'条件 skip 不计数据库集成通过；逐 Test 原因在 events.jsonl'},
 {'kind':'SonarQube','status':'pending_root_rescan','reason':'统一平台复扫归 root 负责'}]
batch=json.loads((report/'go-server-batch04-tests.json').read_text(encoding='utf-8'))
frozen=json.loads((report/'go-server-batch04-source-sha.json').read_text(encoding='utf-8'))['files']
assert batch['exit_code']==0 and not batch['source_changed_during_test']
test_results={x['Test']:x for x in batch['all_test_results'] if x.get('Test')}
ledger['failed_validation_attempts_preserved']=['go-server-batch02-tests.json','go-server-batch03-tests.json']
ledger['current_dynamic_scope']={'package_compiled':True,'passed_tests':188,'conditional_skips':62,'postgres_business_test_pass_count':0,'source_sha_evidence':'go-server-batch04-source-sha.json','no_source_change_during_tests':True,'no_database_environment_was_cleared':not batch['cleared_environment_names']}
events=[json.loads(line) for line in (report/'go-server-batch04-events.jsonl').read_text(encoding='utf-8').splitlines()]
outputs=collections.defaultdict(list)
for event in events:
 if event.get('Test') and event.get('Output'):
  outputs[event['Test']].append(event['Output'])
skip_records=[]
for result in batch['all_test_results']:
 if result['Action']!='skip':continue
 name=result['Test'];text=''.join(outputs[name])
 category='auxiliary_entry_condition' if name=='TestBusinessCommandReservationCrashHelper' else 'postgres_environment_condition'
 assert category=='auxiliary_entry_condition' or ('EDUGRADE_E2E_DATABASE_URL' in text or 'PostgreSQL' in text),name
 skip_records.append({'test':name,'category':category,'original_output':outputs[name]})
skip_counts=dict(collections.Counter(record['category'] for record in skip_records))
assert skip_counts=={'postgres_environment_condition':61,'auxiliary_entry_condition':1}
skip_evidence={'source_events':'go-server-batch04-events.jsonl','counts':skip_counts,'tests':skip_records,'original_logs_unchanged':True}
(report/'go-server-batch04-skip-classification.json').write_text(json.dumps(skip_evidence,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
ledger['current_dynamic_scope'].update(postgres_conditional_skips=61,auxiliary_entry_conditional_skips=1,skip_classification='go-server-batch04-skip-classification.json')
for validation in ledger['validations']:
 if validation['kind']=='Go compilation and existing tests':
  validation.update(postgres_conditional_skips=61,auxiliary_entry_conditional_skips=1,skip_classification='go-server-batch04-skip-classification.json')
for file,record in ledger['files'].items():
 valid=record['current_sha256']==frozen[file]
 names=re.findall(r'^func (Test\w+)\(', (root/file).read_text(encoding='utf-8'),re.M)
 record['package_validation']={'compiled_for_current_sha':valid,'evidence':'go-server-batch04-tests.json','tests_in_file':[test_results[name] for name in names if name in test_results]}
 if any(x['path']==file for x in question_bank['files']):
  record.update(review_status='delegated_read_and_refactored',review_scope='题库智能体完整审读，独立来源见 go-server-question-bank-progress.json')
for issue in ledger['issues']:
 issue['compilation_validation']={'status':'passed_for_current_sha' if issue['current_sha256']==frozen[issue['path']] else 'source_changed_since_batch04','evidence':'go-server-batch04-tests.json','source_sha':'go-server-batch04-source-sha.json','postgres_condition_skip_not_counted_as_pass':True}
path.write_text(json.dumps(ledger,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
# 比较原有 Fatal/Error 检查数量和消息：提取后允许函数排列不同，不能删掉断言。
backup=Path('D:/tools/sonar-remediation-20261011/server-extra-baseline');assertions=[]
for p in sorted(backup.rglob('*.go')):
 relative=p.relative_to(backup);current=root/relative
 before=p.read_text(encoding='utf-8');after=current.read_text(encoding='utf-8')
 pattern=r'\bt\.(?:Fatalf|Fatal|Errorf|Error)\s*\('
 counts={'before':len(re.findall(pattern,before)),'after':len(re.findall(pattern,after))}
 assertions.append({'file':str(relative).replace('\\','/'),'guard_calls':counts,'count_retained':counts['before']==counts['after']})
(report/'go-server-assertion-inventory.json').write_text(json.dumps({'scope':'断言数量辅助核对；消息/条件和顺序仍按每次源码审读核对，不能替代测试','files':assertions},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
md=f'''# Go server 专项进度

62 项原告警：本智能体已修改 {ledger['summary'].get('implemented_needs_validation',0)} 项；题库 5 项由 Go 智能体完成独立账本，源码总计 {ledger['source_changed_original_issues']} 项。已实现不代表平台关闭或测试通过。

7 个长参数列表改为真实领域输入：仲裁夹具、文件资产、题库请求、题目配置、评分摘要、角色身份和校准证据。51 个调用同步改为具名字段，原值及角色独立性保留。

复杂度按实际业务阶段拆解：申诉派发与教师建议、仲裁回执与并发导出、命令占位接管与终态恢复、逐项人工评分、发布前确认门禁、学校与 OCR 权限场景、数学冻结量规与历史修订、MFA 通知回滚/并发禁用/审计、迁移升级的数据/密文/绑定审计、模型探测生命周期、用户开通幂等性。

所有新测试 helper 使用 t.Helper()；没有新增子测试改变失败传播，也没有通用 FatalIf(bool) 或删除 guard 来降低复杂度。断言数量辅助清单 go-server-assertion-inventory.json；每条业务条件仍按实际代码核对。

当前 gofmt 及 Go AST 解析通过。最新冻结批次 go-server-batch04 已通过 Go 编译和现有 server 测试：188 条 Test/subtest 通过、62 条条件跳过（61 条 PostgreSQL 环境条件，1 条 TestBusinessCommandReservationCrashHelper 辅助入口条件），源码测试前后 SHA 一致，所有改动与本批次 SHA 匹配。逐项原始跳过原因在 go-server-batch04-skip-classification.json，原测试日志未修改。环境本来没有数据库 DSN，没有清除数据库变量；条件跳过不计数据库业务验证通过。batch02/03 编译失败日志及两批修复记录全部保留，不以旧通过批次覆盖新改动。自制复杂度估算不能代替 SonarQube，本次不运行平台扫描。

逐原 issue key 状态见 go-server-progress.json。完整审读的 {len(fully_read)} 个文件记录为 annotated，其余文件仍 pending；参数调用更新不等同全文件完成审读。每份写入前源码备份在 server-extra-baseline，保留已有工作区修改。
'''
(report/'go-server-progress.md').write_text(md,encoding='utf-8');print(json.dumps(ledger['summary']));print('assertion mismatches',sum(not x['count_retained'] for x in assertions))
