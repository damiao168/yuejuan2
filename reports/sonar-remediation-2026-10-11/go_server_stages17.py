from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply']
path='services/api-gateway/internal/server/e2e_story061_governance_test.go';name='TestStory061GovernanceFoundationE2EWithPostgresTestDatabase'

def helper(name,params,body,comment,result='',aliases=''):
 return '\n\n// '+comment+'\nfunc '+name+'('+params+')'+result+' {\n\tt.Helper()'+aliases+body+'\n}'

def governance(s,body):
 defaults=body.index('\n\tvar mode, fallback string')
 facts=body.index('\n\trequestID :=')
 storage=body.index('\n\tgovernanceStore :=')
 baseline=body.index('\n\tbaseline :=',storage)
 provider=body.index('\n\tcreatedProvider, err :=')
 policy=body.index('\n\tpolicy, err :=')
 evaluation=body.index('\n\tdeployments, err :=')
 guards=body.index('\n\tguardInput :=')
 sandbox=body.index('\n\tdashscopeProvider, err :=')
 isolated=body.index('\n\tnewTenantID :=')
 default_phase=body[defaults:facts];fact_phase=body[facts:storage]
 baseline_phase=body[baseline:provider]+'\n\treturn baseline'
 provider_phase=body[provider:policy]+'\n\treturn createdDeployment'
 policy_phase=body[policy:evaluation]
 evaluation_phase=body[evaluation:guards]+'\n\treturn story061EvaluationFixture{Input: evaluationInput, CandidateInput: candidateInput, LocalDeployment: localDeployment, CreatedDeployment: createdDeployment}'
 guard_phase=body[guards:sandbox]+'\n\treturn guardRun'
 sandbox_phase=body[sandbox:isolated]+'\n\treturn approvalInput'
 isolation_phase=body[isolated:]
 body=body[:defaults]+'''\n\te2eAssertGovernanceDefaultAndSQLConstraints(t, ctx, db, tenantID)
\te2eAssertGovernanceCallFactsAndRBAC(t, ctx, db, tenantID)
\tgovernanceStore := modelgovernance.NewPostgresStore(db)
\tbaseline := e2eRegisterGovernanceLocalBaseline(t, ctx, governanceStore, tenantID)
\tcreatedDeployment := e2eCreateGovernedExternalInventory(t, ctx, governanceStore, tenantID)
\te2eCloseGovernanceExternalPolicy(t, ctx, governanceStore, tenantID)
\tevaluation := e2eCompleteGovernanceOfflineEvaluation(t, ctx, governanceStore, tenantID, baseline, createdDeployment)
\tevaluation.GuardRun = e2eAssertGovernanceEvaluationSQLGuards(t, ctx, db, governanceStore, tenantID, evaluation)
\tapprovalInput := e2eAssertGovernanceSandboxApprovalLifecycle(t, ctx, db, governanceStore, tenantID)
\te2eAssertGovernanceNewTenantIsolation(t, ctx, db, governanceStore, tenantID, evaluation, approvalInput)
'''
 helpers='''

type story061EvaluationFixture struct {
\tInput modelgovernance.EvaluationRunInput
\tCandidateInput func(string) modelgovernance.EvaluationCandidateInput
\tLocalDeployment, CreatedDeployment modelgovernance.Deployment
\tGuardRun modelgovernance.EvaluationRun
}
'''
 sqlparams='t *testing.T, ctx context.Context, db *sql.DB, tenantID string'
 storeparams='t *testing.T, ctx context.Context, governanceStore *modelgovernance.PostgresStore, tenantID string'
 guardparams='t *testing.T, ctx context.Context, db *sql.DB, governanceStore *modelgovernance.PostgresStore, tenantID string'
 evalaliases='\n\tevaluationInput, candidateInput, localDeployment, createdDeployment := evaluation.Input, evaluation.CandidateInput, evaluation.LocalDeployment, evaluation.CreatedDeployment'
 helpers+=helper('e2eAssertGovernanceDefaultAndSQLConstraints',sqlparams,default_phase,'租户模型策略默认关闭外部访问；数据库必须拒绝明文凭据和不兼容适配器，显式授权后也不能保留禁用外部路由。')
 helpers+=helper('e2eAssertGovernanceCallFactsAndRBAC',sqlparams,fact_phase,'同一请求只产生一个可计费调用事实，平台供应商管理与租户策略、评测权限分别核对。')
 helpers+=helper('e2eRegisterGovernanceLocalBaseline',storeparams,baseline_phase,'先登记本地基线，提供商读模型必须隐藏凭据引用，不能把凭据配置误当成公开字段。',' modelgovernance.LocalBaseline')
 helpers+=helper('e2eCreateGovernedExternalInventory',storeparams,provider_phase,'存储层创建外部提供商和部署时保留凭据脱敏及库存绑定核对。',' modelgovernance.Deployment')
 helpers+=helper('e2eCloseGovernanceExternalPolicy',storeparams,policy_phase,'先按当前策略版本关闭外部路由，再用旧版本更新验证乐观锁冲突。')
 helpers+=helper('e2eCompleteGovernanceOfflineEvaluation',storeparams+', baseline modelgovernance.LocalBaseline, createdDeployment modelgovernance.Deployment',evaluation_phase,'离线协议评测冻结本地和外部两个候选；完成后不能追加候选，随后明确作废证据。',' story061EvaluationFixture')
 helpers+=helper('e2eAssertGovernanceEvaluationSQLGuards',guardparams+', evaluation story061EvaluationFixture',guard_phase,'新建独立评测检查数据库护栏：测量不可变、候选不足不能完成、样本数量不能超出冻结范围。',' modelgovernance.EvaluationRun',evalaliases)
 helpers+=helper('e2eAssertGovernanceSandboxApprovalLifecycle',guardparams,sandbox_phase,'沙箱批准必须绑定库存和审阅事实；重复批准、重复撤销冲突，明确撤销后才允许替代批准。',' modelgovernance.SandboxApprovalInput')
 helpers+=helper('e2eAssertGovernanceNewTenantIsolation',guardparams+', evaluation story061EvaluationFixture, approvalInput modelgovernance.SandboxApprovalInput',isolation_phase,'新租户由触发器建立关闭的默认策略，并拒绝跨租户沙箱、评测候选和提供商创建者。',aliases='\n\tguardRun, candidateInput, createdDeployment := evaluation.GuardRun, evaluation.CandidateInput, evaluation.CreatedDeployment')
 s=replace_function(s,name,body,helpers)
 return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,governance)
(report/'go-server-stages17.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
