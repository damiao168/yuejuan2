from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go']
path='services/api-gateway/internal/server/e2e_subjective_panel_test.go'
fields={'ctx':'Context','db':'DB','router':'Router','platformToken':'PlatformToken','fixture':'Acceptance','suffix':'Suffix','panels':'Panels','create':'Create','answerVersion':'AnswerVersion','segmentID':'SegmentID','questionID':'QuestionID','panel':'Panel','runInput':'RunInput','run':'PrimaryA','bRun':'PrimaryB','score':'Score','evaluations':'Evaluations','evalRun':'Evaluation','policyService':'PolicyService','policyVersion':'PolicyVersion','readyRun':'ReadyEvaluation','modelConfigs':'ModelConfigs'}

def helper(name,phase,comment,returns='',locals=()):
 used=[(key,value) for key,value in fields.items() if key not in locals and re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('scenario.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, scenario subjectivePanelScenario)'+returns+' {\n\tt.Helper()'+aliases+phase+'\n}'

def transform(s,body):
 start=body.index('\n\tpanel, err :=');wrong=body.index('\n\tif _, err := panels.UpdatePanel',start)
 second=body.index('\n\tbInput :=');complete=body.index('\n\tcompleteRole :=');resolve=body.index('\n\tgap, score :=')
 human=body.index('\n\thumanInput :=');evaluation=body.index('\n\tevaluations :=');policy=body.index('\n\tpolicyVersion :=')
 ready=body.index('\n\treadyRun, err :=');approve=body.index('\n\treadyPolicyVersion :=');configs=body.index('\n\tmodelConfigs :=');roles=body.index('\n\troleStore :=')
 phases=[body[start:wrong]+'\n\treturn panel, runInput, run',body[wrong:second].replace('if _, err = panels.GetOrCreateRun','if _, err := panels.GetOrCreateRun'),body[second:complete]+'\n\treturn bRun',body[complete:resolve],body[resolve:human]+'\n\treturn score',body[human:evaluation],body[evaluation:policy]+'\n\treturn evaluations, evalRun',body[policy:ready]+'\n\treturn policyService, policyVersion',body[ready:approve]+'\n\treturn readyRun',body[approve:configs],body[configs:roles]+'\n\treturn modelConfigs',body[roles:]]
 declarations='''

type subjectivePanelScenario struct {
 Context context.Context
 DB *sql.DB
 Router http.Handler
 PlatformToken, Suffix string
 Acceptance story056AcceptanceFixture
 Panels *subjective.PostgresStore
 Create subjective.CreatePanelInput
 AnswerVersion, SegmentID, QuestionID string
 Panel subjective.GradingPanel
 RunInput subjective.CreateRunInput
 PrimaryA, PrimaryB subjective.GradingRun
 Score float64
 Evaluations *gradingevaluation.PostgresStore
 Evaluation, ReadyEvaluation gradingevaluation.Run
 PolicyService *subjective.PanelPolicyService
 PolicyVersion string
 ModelConfigs []string
}'''
 specs=[
 ('e2eCreateAndClaimPrimaryPanelRole','同一答案版本只创建一个面板，角色 A 的运行保持绑定且只能领取一次。',' (subjective.GradingPanel, subjective.CreateRunInput, subjective.GradingRun)',('panel','runInput','run')),
 ('e2eAssertPanelFrozenRoleBoundaries','角色槽位、冻结答案版本和每角色唯一运行同时受存储及数据库约束保护。','',()),
 ('e2eClaimSecondaryPanelRole','角色 B 使用独立模型与请求；两份角色尚未完成时不能进入比较状态。',' subjective.GradingRun',('bRun',)),
 ('e2eCompletePrimaryPanelGrades','依次保存 A、B 的影子建议并完成原角色运行；此处仍不写生产成绩。','',()),
 ('e2eResolveAndReplayPanel','两份主角色完成后才解析结果；终态不能回退，原答案重放及满分身份保持冻结。',' float64',('score',)),
 ('e2eAssertPanelHumanFallbackVersions','同一面板的人工作业幂等，更新答案版本必须产生新的面板和复核任务。','',()),
 ('e2ePersistSinglePanelEvaluation','评测观测、指标和完成事实来自同一持久化运行，保留独立人工参考的证据。',' (*gradingevaluation.PostgresStore, gradingevaluation.Run)',('evaluations','evalRun')),
 ('e2eAssertPanelInsufficientEvidenceClosed','样本与仲裁证据不足时策略保留影子状态，不能批准真实评分策略。',' (*subjective.PanelPolicyService, string)',('policyService','policyVersion')),
 ('e2eCreateReadyPanelEvidence','按原顺序写入共识与仲裁样本，再冻结完成的评测运行。',' gradingevaluation.Run',('readyRun',)),
 ('e2eApproveAndRetirePanelPolicy','批准冻结模型集合及评测依据；先退役策略才能作废其已引用评测。','',()),
 ('e2eSeedIndependentPanelCredentials','同学校同供应商同模型可以保存独立密文凭据；三个评分角色仍使用独立配置。',' []string',('modelConfigs',)),
 ('e2eAssertPanelGovernedBindingsAndActor','角色强度与配置保持原绑定，跨学校平台操作保留平台操作者归属。','',())]
 helpers=declarations+''.join(helper(name,phase,comment,returns,locals) for phase,(name,comment,returns,locals) in zip(phases,specs))
 body=body[:start]+'''
 scenario := subjectivePanelScenario{Context: ctx, DB: db, Router: router, PlatformToken: platformToken,
  Acceptance: fixture, Suffix: suffix, Panels: panels, Create: create, AnswerVersion: answerVersion,
  SegmentID: segmentID, QuestionID: questionID}
 scenario.Panel, scenario.RunInput, scenario.PrimaryA = e2eCreateAndClaimPrimaryPanelRole(t, scenario)
 e2eAssertPanelFrozenRoleBoundaries(t, scenario)
 scenario.PrimaryB = e2eClaimSecondaryPanelRole(t, scenario)
 e2eCompletePrimaryPanelGrades(t, scenario)
 scenario.Score = e2eResolveAndReplayPanel(t, scenario)
 e2eAssertPanelHumanFallbackVersions(t, scenario)
 scenario.Evaluations, scenario.Evaluation = e2ePersistSinglePanelEvaluation(t, scenario)
 scenario.PolicyService, scenario.PolicyVersion = e2eAssertPanelInsufficientEvidenceClosed(t, scenario)
 scenario.ReadyEvaluation = e2eCreateReadyPanelEvidence(t, scenario)
 e2eApproveAndRetirePanelPolicy(t, scenario)
 scenario.ModelConfigs = e2eSeedIndependentPanelCredentials(t, scenario)
 e2eAssertPanelGovernedBindingsAndActor(t, scenario)
'''
 return replace_function(s,'TestPostgresSubjectivePanelAndEvaluationRoundTrip',body,helpers).replace('"context"','"context"\n\t"database/sql"',1)
apply(path,'TestPostgresSubjectivePanelAndEvaluationRoundTrip',transform)
(report/'go-server-stages23.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
