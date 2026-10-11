from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply']

path='services/api-gateway/internal/server/e2e_math_rubric_score_test.go';name='TestPostgresMathRubricScoreUsesOnlyFrozenExamRubric'
def frozen_rows(s,body):
 start=body.index('\n\tfor _, seed := range');end=body.index('\n\tcontract := mathunderstanding.CreateArtifactInput',start)
 phase=body[start:end]
 identity='mathRubricSnapshotIdentity{Tenant: tenant, Exam: exam, Question: question, Segment: segment, Snapshot: snapshot, CropHash: cropHash}'
 body=body[:start]+'\n\tseedFrozenMathRubricRows(t, ctx, db, '+identity+', frozen, encode)'+body[end:]
 helper='\n\ntype mathRubricSnapshotIdentity struct {\n\tTenant, Exam, Question, Segment, Snapshot, CropHash string\n}\n\n// 同时保存冻结量规与故意错误的活动量规，后续预览才能区分两种来源。\nfunc seedFrozenMathRubricRows(t *testing.T, ctx context.Context, db *sql.DB, identity mathRubricSnapshotIdentity, frozen paper.Rubric, encode func(any) []byte) {\n\tt.Helper()\n\ttenant, exam, question, segment, snapshot, cropHash := identity.Tenant, identity.Exam, identity.Question, identity.Segment, identity.Snapshot, identity.CropHash'+phase+'\n}'
 helper=helper.replace('if _, err = db.ExecContext','if _, err := db.ExecContext')
 s=replace_function(s,name,body,helper);return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,frozen_rows)

path='services/api-gateway/internal/server/e2e_math_effective_evidence_test.go';name='TestPostgresMathEffectiveEvidenceProjection'
def history_projection(s,body):
 start=body.index('\n\tcontract.SolutionGraph.Steps[0].NormalizedText = "latest correction')
 phase=body[start:].replace('_, err = db.ExecContext','_, err := db.ExecContext',1).replace('effective, err = mathunderstanding.ResolveEffectiveArtifact','effective, err := mathunderstanding.ResolveEffectiveArtifact',1)
 fixture='mathCorrectionHistoryFixture{TenantID: tenantID, SegmentID: segmentID, ArtifactID: artifactID, ActorID: actorID, Contract: contract, Input: input}'
 body=body[:start]+'\n\tassertMathCorrectionHistoryProjection(t, ctx, db, artifacts, corrections, '+fixture+', encode)\n'
 helper='\n\ntype mathCorrectionHistoryFixture struct {\n\tTenantID, SegmentID, ArtifactID, ActorID string\n\tContract mathunderstanding.CreateArtifactInput\n\tInput mathunderstanding.CreateCorrectionInput\n}\n\n// 历史列表最多返回 500 条，最新有效修订仍须读到第 503 条；随后检验租户隔离和过时产物拒绝。\nfunc assertMathCorrectionHistoryProjection(t *testing.T, ctx context.Context, db *sql.DB, artifacts *mathunderstanding.PostgresStore, corrections *mathunderstanding.PostgresCorrectionStore, fixture mathCorrectionHistoryFixture, encode func(any) []byte) {\n\tt.Helper()\n\ttenantID, segmentID, artifactID, actorID := fixture.TenantID, fixture.SegmentID, fixture.ArtifactID, fixture.ActorID\n\tcontract, input := fixture.Contract, fixture.Input'+phase+'}'
 s=replace_function(s,name,body,helper);return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,history_projection)
(report/'go-server-stages4.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions']},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
