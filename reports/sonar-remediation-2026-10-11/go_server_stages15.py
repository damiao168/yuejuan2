from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply'];callback=ns['callback']
path='services/api-gateway/internal/server/e2e_math_verification_runtime_test.go';name='TestPostgresMathVerificationRuntimeAtomicActivation'

def verification(s,body):
 body=body.replace('\n\tfor _, scenario :=','\n\tfixture := mathVerificationRuntimeFixture{DB: db, Artifacts: artifacts, Corrections: corrections, Runtime: runtime, Handler: handler}\n\tfor _, scenario :=',1)
 body,case=callback(s,body,'t.Run(scenario','e2eRunMathVerificationActivationScenario','t *testing.T, ctx context.Context, fixture mathVerificationRuntimeFixture, scenario string','t, ctx, fixture, scenario','原三个子测试分别保留无效租约、正常激活和新人工修订，逐个创建独立租户后运行原回调。')
 _,_,casebody=function(case,'e2eRunMathVerificationActivationScenario');casebody=re.sub(r'^\t','',casebody.removeprefix('\n\tt.Helper()'),flags=re.M)
 tasks=casebody.index('\ntasks, err := runtime.Claim')
 seed=casebody[:tasks]
 result=casebody.index('\nif scenario == "invalid_lease" {',casebody.index('\nstoredTask, err :='))
 result_phase=casebody[result:]
 casebody='\n\tstate, encode := e2ePrepareMathVerificationActivation(t, ctx, fixture)\n\ttenantID, actorID, segmentID, parentID, contract := state.TenantID, state.ActorID, state.SegmentID, state.ParentID, state.Contract'+casebody[tasks:result]+'\n\te2eAssertMathVerificationActivationOutcome(t, ctx, fixture, state, mathVerificationRuntimeOutcome{Current: current, Task: storedTask, Recorder: rec}, scenario)\n'
 aliases='\n\tdb, artifacts, corrections, runtime, handler := fixture.DB, fixture.Artifacts, fixture.Corrections, fixture.Runtime, fixture.Handler'
 case=replace_function(case,'e2eRunMathVerificationActivationScenario','\n\tt.Helper()'+aliases+casebody,'')
 success_index=result_phase.rfind('} else {')
 success=result_phase[success_index+len('} else {'):result_phase.rfind('}')]
 result_phase=result_phase[:success_index]+'} else {\n\te2eAssertActivatedMathVerificationHistory(t, ctx, fixture, state, outcome)\n}\n'
 helpers='''

type mathVerificationRuntimeFixture struct {
\tDB *sql.DB
\tArtifacts *mathunderstanding.PostgresStore
\tCorrections *mathunderstanding.PostgresCorrectionStore
\tRuntime *workerruntime.PostgresStore
\tHandler *mathunderstanding.Handler
}

type mathVerificationActivationScenario struct {
\tTenantID, ActorID, SegmentID, SnapshotID, ParentID string
\tContract mathunderstanding.CreateArtifactInput
}

type mathVerificationRuntimeOutcome struct {
\tCurrent mathunderstanding.Artifact
\tTask workerruntime.Task
\tRecorder *httptest.ResponseRecorder
}
'''+case
 helpers+='\n\n// 同一场景先建立租户、答案和不可变识别产物；原 JSON 编码闭包继续用于随后的完成回调。\nfunc e2ePrepareMathVerificationActivation(t *testing.T, ctx context.Context, fixture mathVerificationRuntimeFixture) (mathVerificationActivationScenario, func(any) []byte) {\n\tt.Helper()\n\tdb := fixture.DB'+seed+'\n\treturn mathVerificationActivationScenario{TenantID: tenantID, ActorID: actorID, SegmentID: segmentID, SnapshotID: snapshotID, ParentID: parentID, Contract: contract}, encode\n}'
 helpers+='\n\n// 回调后无效租约保持 leased，新修订使旧回调 superseded，只有正常场景激活新的验证产物。\nfunc e2eAssertMathVerificationActivationOutcome(t *testing.T, ctx context.Context, fixture mathVerificationRuntimeFixture, state mathVerificationActivationScenario, outcome mathVerificationRuntimeOutcome, scenario string) {\n\tt.Helper()\n\tcurrent, storedTask, rec, parentID := outcome.Current, outcome.Task, outcome.Recorder, state.ParentID'+result_phase+'\n}'
 helpers+='\n\n// 激活只替换当前指针，原识别产物仍非 current 且不补写验证内容，保持历史证据不可变。\nfunc e2eAssertActivatedMathVerificationHistory(t *testing.T, ctx context.Context, fixture mathVerificationRuntimeFixture, state mathVerificationActivationScenario, outcome mathVerificationRuntimeOutcome) {\n\tt.Helper()\n\tartifacts, tenantID, parentID := fixture.Artifacts, state.TenantID, state.ParentID\n\tcurrent, storedTask, rec := outcome.Current, outcome.Task, outcome.Recorder'+success+'\n}'
 s=replace_function(s,name,body,helpers)
 return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,verification)
(report/'go-server-stages15.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
