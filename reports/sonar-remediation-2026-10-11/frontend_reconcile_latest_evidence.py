from pathlib import Path
import hashlib,json

root=Path('D:/project/yuejuan')
report=root/'reports/sonar-remediation-2026-10-11'
ledger_path=report/'frontend-progress.json'
ledger=json.loads(ledger_path.read_text(encoding='utf-8'))
contracts=json.loads((report/'frontend-production-contracts-current-01.json').read_text(encoding='utf-8'))
assert contracts['exit_code']==0 and not contracts['changed_sources']
assert contracts['source_sha_before']==contracts['source_sha_after']
tap=(report/'frontend-production-contracts-current-01.tap').read_text(encoding='utf-8')
assert 'tests 17' in tap and 'pass 17' in tap and 'fail 0' in tap and 'skipped 0' in tap
snapshot=json.loads((report/'full-checkpoint-03/source-scope.json').read_text(encoding='utf-8'))
snapshot_files={item['path']:item['sha256'] for item in snapshot['files']}
evidence_by_file={
 'apps/web-admin/scripts/check-production-routes.mjs':['frontend-production-contracts-current-01.json','frontend-production-contracts-current-01.tap'],
 'apps/web-admin/src/components/ResponsiveTable.tsx':['frontend-web-retry-01-execution.json','frontend-web-current-typecheck-02.json'],
 'docs/architecture/tools/capture_preview_qa.js':['root-platform-disposition-results.json','delivery-independent-preflight-audit-python-agent.json'],
 'docs/architecture/tools/render_native.js':['root-new-js-tests-02.json','root-platform-disposition-results.json','root-platform-disposition-phase02-results.json'],
 'scripts/check-story052-production-deployment.mjs':['root-new-findings-progress.json','root-production-deployment-check-01.log'],
}
reconciled=[]
for file,evidence in evidence_by_file.items():
 current_sha=hashlib.sha256((root/file).read_bytes()).hexdigest()
 assert snapshot_files[file]==current_sha,file
 record=ledger['files'][file]
 previous=record.get('current_sha256',record.get('sha256'))
 record.update(latest_current_sha=current_sha,latest_current_sha_evidence=evidence+['full-checkpoint-03/source-scope.json'],historical_sha_fields_preserved=True)
 for issue in ledger['issues']:
  if issue['file']==file:
   issue.update(latest_current_sha=current_sha,latest_current_sha_evidence=evidence+['full-checkpoint-03/source-scope.json'])
 reconciled.append({'file':file,'historical_recorded_sha256':previous,'latest_current_sha256':current_sha,'evidence':evidence,'checkpoint03_frozen_sha_matches':True,'historical_validation_not_reassigned':True})
name='最新生产路由契约与 measure-build 现有测试'
ledger['validations']=[entry for entry in ledger['validations'] if entry['name']!=name]
ledger['validations'].append({'name':name,'result':'passed_for_current_frozen_sha','tests':17,'skips':0,'exit_code':0,'source_sha_stable':True,'evidence':'frontend-production-contracts-current-01.json','test_output':'frontend-production-contracts-current-01.tap','scope':'生产路由契约与 measure-build；不扩大到浏览器验收或未运行的其他测试'})
ledger_path.write_text(json.dumps(ledger,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
(report/'frontend-latest-sha-reconciliation.json').write_text(json.dumps({'scope':'5 个旧 SHA 元数据文件新增当前 SHA 与独立证据，历史测试 SHA 不改写','files':reconciled,'source_files_edited':False,'platform_final_result_pending':True},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
md_path=report/'frontend-progress.md'
md=md_path.read_text(encoding='utf-8')
marker='\n最新证据补充：'
if marker in md:md=md[:md.index(marker)]
md+='\n最新证据补充：生产路由契约与 measure-build 的 17 项现有测试重新运行退出 0、0 跳过，测试前后源码 SHA 稳定；证据 frontend-production-contracts-current-01.json/tap。5 个历史元数据 SHA 与当前源码不同的文件已增加 latest_current_sha 和当前独立证据，详见 frontend-latest-sha-reconciliation.json；历史通过 SHA 保留，不把历史测试转记为后续源码通过。最终平台结果待 root 导出 checkpoint03。\n'
md_path.write_text(md,encoding='utf-8')
print(json.dumps({'latest_sha_files':len(reconciled),'production_tests':17,'production_exit_code':0}))
