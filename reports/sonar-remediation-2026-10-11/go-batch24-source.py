"""批处理幂等、动态任务执行与 V2 契约验证按业务阶段拆分，保持错误优先级。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
base = Path("services/api-gateway/internal/subjective")
changes = {}

path = base / "scoring_run_batch_test.go"
source = path.read_text(encoding="utf-8")
function = source.index("func TestScoringRunBatchIdentityAndRetryCandidates(")
begin = source.index("\tfor _, tc := range []struct", function)
end = source.index('\n\tif _, err := store.UpdateBatch(ctx, tenant, batch.ID, UpdateBatchInput{Status: "cancelled"})', begin)
retry_body = source[begin:end]
cancel_begin = end + 1
cancel_end = source.index("\n}\n", cancel_begin)
cancel_body = source[cancel_begin:cancel_end]
source = source[:begin] + "\tassertScoringRunRetryCandidateBoundaries(t, ctx, store, tenant, batch, other)\n\tassertCancelledScoringBatchStaysTerminal(t, ctx, store, tenant, batch)" + source[cancel_end:]
source += "\n// 重试候选仅来自同一评分运行的失败/冲突项，历史无绑定批次仍拒绝重试。\nfunc assertScoringRunRetryCandidateBoundaries(t *testing.T, ctx context.Context, store *MemoryStore, tenant string, batch, other GradingBatch) {\n\tt.Helper()\n" + retry_body + "\n}\n"
source += "\nfunc assertCancelledScoringBatchStaysTerminal(t *testing.T, ctx context.Context, store *MemoryStore, tenant string, batch GradingBatch) {\n\tt.Helper()\n" + cancel_body + "\n}\n"
path.write_text(source, encoding="utf-8")
changes[str(path).replace("\\", "/")] = "分离批次身份/租户隔离、失败候选与取消终态断言；保持创建、更新、读取与候选排序原执行顺序。"

path = base / "handlers_test.go"
source = path.read_text(encoding="utf-8")
function = source.index("func TestSubjectiveBatchCreationValidatesSegmentsAndIsIdempotent(")
begin = source.index('\tenqueueReq := httptest.NewRequest', function)
end = source.index('\n\tsecond := request(', begin)
body = source[begin:end].replace("created.Batch.ID", "batchID")
source = source[:begin] + "\tassertSubjectiveBatchEnqueueAndLiveProgress(t, handler, store, runtime, created.Batch.ID)" + source[end:]
source += "\n// 入队重放、真实租约执行和实时统计依次验证；领取任务后领域运行须等执行才进入处理中。\nfunc assertSubjectiveBatchEnqueueAndLiveProgress(t *testing.T, handler *subjective.Handler, store *subjective.MemoryStore, runtime *workerruntime.MemoryStore, batchID string) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")
changes[str(path).replace("\\", "/")] = "分离批次入队重放与租约执行/实时进度阶段；保留批次创建、请求变化冲突和重复 segment 拒绝断言。"

path = base / "grading_agent_v2_contract_test.go"
source = path.read_text(encoding="utf-8")
begin = source.index("func validateGradingAgentV2RequestFixture(")
end = source.index("\nfunc gradingAgentV2BBox(", begin)
original = source[begin:end]
request_start = original.index('\texpected := []string{')
constraint_start = original.index('\tconstraint, ok := request["output_constraint"]')
math_start = original.index('\tmathEvidence, ok := request["math_evidence"]')
media_start = original.index('\tmedia, ok := request["media_evidence"]')
dimensions_start = original.index('\tsize, err := v2JSONInt')
bbox_start = original.index('\tbbox, units, err := gradingAgentV2BBox')
png_start = original.index('\tencoded, ok := media["data_base64"]')
binding_start = original.index('\texpectedBinding, err := gradingAgentV2BindingHash')
request_end = original.index('\n}\n', binding_start)
governance = original[request_start:constraint_start]
constraint = original[constraint_start:math_start]
math = original[math_start:media_start]
media_header = original[media_start:dimensions_start]
dimensions = original[dimensions_start:bbox_start]
bbox = original[bbox_start:png_start]
png = original[png_start:binding_start]
binding = original[binding_start:request_end]

response_start = original.index("func validateGradingAgentV2ResponseFixture(")
response_body = original[response_start:]
evidence_start = response_body.index('\tmathEvidence := request["math_evidence"]')
rubric_start = response_body.index('\trubric := request["rubric"]')
candidate_start = response_body.index('\t\tcandidate, ok := raw.(map[string]any)')
candidate_end = response_body.index('\n\t}\n\treturn nil\n}', candidate_start)
response_header = response_body[response_body.index('\tif err := exactV2Fields('):evidence_start]
evidence = response_body[evidence_start:rubric_start]
candidate = response_body[candidate_start:candidate_end]
candidate = "\n".join(line[1:] if line.startswith("\t") else line for line in candidate.splitlines())
response_middle = response_body[rubric_start:candidate_start]

new = '''// 夹具验证按治理、证据和二进制绑定顺序执行，首个失败仍返回原错误。
func validateGradingAgentV2RequestFixture(request map[string]any) error {
    if err := validateGradingV2RequestGovernance(request); err != nil { return err }
    if err := validateGradingV2OutputAuthority(request); err != nil { return err }
    if err := validateGradingV2MathEvidenceEnvelope(request); err != nil { return err }
    media, err := gradingV2FixtureMediaEnvelope(request)
    if err != nil { return err }
'''
new += dimensions + bbox
new += '''    if err := validateGradingV2FixturePNG(media, size, width, height); err != nil { return err }
'''
new += binding + "\n}\n"
new += "\nfunc validateGradingV2RequestGovernance(request map[string]any) error {\n" + governance + "\treturn nil\n}\n"
new += "\nfunc validateGradingV2OutputAuthority(request map[string]any) error {\n" + constraint + "\treturn nil\n}\n"
new += "\nfunc validateGradingV2MathEvidenceEnvelope(request map[string]any) error {\n" + math + "\treturn nil\n}\n"
new += "\nfunc gradingV2FixtureMediaEnvelope(request map[string]any) (map[string]any, error) {\n" + media_header.replace("return errors.New(", "return nil, errors.New(").replace("return err", "return nil, err") + "\treturn media, nil\n}\n"
new += "\nfunc validateGradingV2FixturePNG(media map[string]any, size, width, height int) error {\n" + png + "\treturn nil\n}\n"
new += "\nfunc validateGradingAgentV2ResponseFixture(response map[string]any, request map[string]any) error {\n\tif err := validateGradingV2ResponseGovernance(response, request); err != nil { return err }\n\tknownEvidence, err := gradingV2FixtureKnownEvidence(request)\n\tif err != nil { return err }\n" + response_middle
new += "\t\tif err := validateGradingV2CriterionCandidate(raw, knownPoints, knownEvidence, seen); err != nil { return err }\n\t}\n\treturn nil\n}\n"
new += "\nfunc validateGradingV2ResponseGovernance(response, request map[string]any) error {\n" + response_header + "\treturn nil\n}\n"
new += "\nfunc gradingV2FixtureKnownEvidence(request map[string]any) (map[string]bool, error) {\n" + evidence.replace("return fmt.Errorf(", "return nil, fmt.Errorf(").replace("return errors.New(", "return nil, errors.New(") + "\treturn knownEvidence, nil\n}\n"
new += "\n// 重复点标记先于链接校验，保持原候选遍历与首个失败的顺序。\nfunc validateGradingV2CriterionCandidate(raw any, knownPoints, knownEvidence, seen map[string]bool) error {\n" + candidate + "\n\treturn nil\n}\n"
source = source[:begin] + new + source[end:]
path.write_text(source, encoding="utf-8")
changes[str(path).replace("\\", "/")] = "V2 夹具请求/响应按治理字段、服务器评分权、数学证据、PNG 完整性及候选链接分解；保留原错误优先级、短路、边界、去重标记顺序。"
spec = importlib.util.spec_from_file_location("progress", root / "go-record-progress.py")
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch24", changes, 4)
