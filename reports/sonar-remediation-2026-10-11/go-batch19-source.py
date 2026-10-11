"""考试归档/命令事务与 OpenAPI 合同按原业务阶段分解。"""
from pathlib import Path
from importlib.util import spec_from_file_location, module_from_spec

ROOT = Path(__file__).resolve().parent
spec = spec_from_file_location("reviewed", ROOT / "go-refactor-reviewed-tests.py")
reviewed = module_from_spec(spec)
spec.loader.exec_module(reviewed)

path = Path("services/api-gateway/internal/exam/session_postgres_batch49_test.go")
reviewed.named_case(path, "TestBatch49PostgresSessionReplayAndUniqueConflictCheckOriginalHash", "tc", "examSessionReplayCase")
reviewed.scenario_stage(path, "TestBatch49PostgresSessionReplayAndUniqueConflictCheckOriginalHash", "assertExamSessionCommandReplayHash", "tc examSessionReplayCase", "tc")
reviewed.scenario_stage(path, "TestBatch49PostgresSessionKeepsChildRosterBlueprintOrderAndAtomicFailure", "assertExamSessionChildrenAtomicity", "failSecond bool", "failSecond")
source = path.read_text(encoding="utf-8")
function = source.index("func assertExamSessionChildrenAtomicity(")
begin = source.index("\n\tfor index, child := range got.Exams", function) + 1
end = source.index('\n\tassertExamBatch29Script(t, script, "commit")', begin)
body = source[begin:end].replace("got.Exams", "children")
source = source[:begin] + "\tassertExamSessionChildRosterMetadata(t, got.Exams)" + source[end:]
source += "\n// 子科依次继承考次信息；第二科的显式班级覆盖父考次班级。\nfunc assertExamSessionChildRosterMetadata(t *testing.T, children []Exam) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/exam/management_archive_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestManagementArchivePreservesSubjectStagesAndBlocksChildWrites(")
begin = source.index("\n\tfor _, child := range archived.Exams", function) + 1
end = source.index("\n\trestored, err :=", begin)
body = source[begin:end]
source = source[:begin] + "\tassertArchivedSubjectWritesBlocked(t, s, ctx, scope, archived, item)" + source[end:]
source += "\n// 整场归档保留学科原阶段和版本，但修改名称、阶段及名单都应被锁定。\nfunc assertArchivedSubjectWritesBlocked(t *testing.T, s *MemoryStore, ctx context.Context, scope auth.AccessScope, archived ManagementExam, item ManagementExam) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/apicontract/math_understanding_openapi_test.go")
source = path.read_text(encoding="utf-8")
stages = [
    ("\tscore := object(t, schemas,", "\tcandidate := object(t, schemas,", "assertMathScoringPreviewAuthority", "评分预览不能获得最终成绩权限，不确定依据必须允许空分数。"),
    ("\tcandidate := object(t, schemas,", "\tgradeProperties := object(t,", "assertMathModelCandidateAuthority", "模型只能提出语义候选，不得自行发明任何评分字段。"),
    ("\tgradeProperties := object(t,", "\tunderstanding := object(t,", "assertMathSubjectiveSuggestionBinding", "主观题建议必须绑定不可变数学证据并保留人工复核要求。"),
    ("\tunderstanding := object(t,", "\tartifactProperties := object(t,", "assertMathUnderstandingEvidenceAndSDK", "有效证据在HTTP与SDK中保持有类型的可空数组和修订字段。"),
]
helpers = []
for start, stop, helper, comment in stages:
    begin = source.index("\n" + start) + 1
    end = source.index("\n" + stop, begin)
    body = source[begin:end]
    source = source[:begin] + "\t" + helper + "(t, schemas)" + source[end:]
    helpers.append("\n// " + comment + "\nfunc " + helper + "(t *testing.T, schemas map[string]any) {\n\tt.Helper()\n" + body + "\n}\n")
begin = source.index("\n\tartifactProperties := object(t,") + 1
end = source.index("\n}\n", begin)
body = source[begin:end]
source = source[:begin] + "\tassertMathArtifactMetadataAndPilotSubjectBoundaries(t, schemas)" + source[end:]
helpers.append("\n// 证据修订元数据、公式候选与试点学科边界共同约束可追溯的建议能力。\nfunc assertMathArtifactMetadataAndPilotSubjectBoundaries(t *testing.T, schemas map[string]any) {\n\tt.Helper()\n" + body + "\n}\n")
source += "".join(helpers)
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/apicontract/exam_command_handler_schema_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func validateOpenAPIValue(")
object_begin = source.index('\n\tcase "object":', function)
array_begin = source.index('\n\tcase "array":', object_begin)
string_begin = source.index('\n\tcase "string":', array_begin)
switch_end = source.index("\n\t}\n\treturn nil\n}", string_begin)
object_body = source[object_begin + len('\n\tcase "object":'):array_begin]
array_body = source[array_begin + len('\n\tcase "array":'):string_begin]
primitive_body = source[string_begin:switch_end]
dedent = lambda body: "\n".join(line[1:] if line.startswith("\t") else line for line in body.strip("\n").splitlines())
object_body = dedent(object_body)
array_body = dedent(array_body)
primitive_body = dedent(primitive_body)
source = source[:object_begin] + '\n\tcase "object":\n\t\treturn validateOpenAPIObject(document, schema, value, location)\n\tcase "array":\n\t\treturn validateOpenAPIArray(document, schema, value, location)\n\tdefault:\n\t\treturn validateOpenAPIPrimitive(schema, value, location)' + source[switch_end:]
# switch 每个分支返回，移除原来的不可达返回。
source = source.replace('\tdefault:\n\t\treturn validateOpenAPIPrimitive(schema, value, location)\n\t}\n\treturn nil\n}', '\tdefault:\n\t\treturn validateOpenAPIPrimitive(schema, value, location)\n\t}\n}', 1)
fields_begin = object_body.index("\trequiredFields, _ :=")
fields_end = object_body.index("\tfor name, property := range properties", fields_begin)
fields_body = object_body[fields_begin:fields_end]
object_body = object_body[:fields_begin] + "\tif err := validateOpenAPIObjectFields(schema, objectValue, properties, location); err != nil {\n\t\treturn err\n\t}\n" + object_body[fields_end:]
source += "\n// 字段存在性和额外字段限制先检查，再递归核验各已提供字段的值。\nfunc validateOpenAPIObject(document map[string]any, schema map[string]any, value any, location string) error {\n" + object_body + "\n\treturn nil\n}\n"
source += "\nfunc validateOpenAPIObjectFields(schema map[string]any, objectValue map[string]any, properties map[string]any, location string) error {\n" + fields_body + "\treturn nil\n}\n"
source += "\nfunc validateOpenAPIArray(document map[string]any, schema map[string]any, value any, location string) error {\n" + array_body + "\n\treturn nil\n}\n"
source += "\nfunc validateOpenAPIPrimitive(schema map[string]any, value any, location string) error {\n\tswitch schema[\"type\"] {\n" + primitive_body + "\n\t}\n\treturn nil\n}\n"
path.write_text(source, encoding="utf-8")

spec = spec_from_file_location("progress", ROOT / "go-record-progress.py")
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch19", {
    "services/api-gateway/internal/exam/session_postgres_batch49_test.go": "完整审读：提取命令哈希重放、子科事务场景与名册元数据校验；保留行锁/唯一冲突恢复、完整SQL顺序和晚失败全回滚。",
    "services/api-gateway/internal/exam/management_archive_test.go": "完整审读：提取归档学科写入锁定核验，保持归档后检查、恢复后原阶段检查和所有原错误断言。",
    "services/api-gateway/internal/apicontract/math_understanding_openapi_test.go": "完整审读：按评分建议权限、模型候选、不可变绑定、SDK证据和试点学科拆分合同核验；全部字段和原判断顺序保留。",
    "services/api-gateway/internal/apicontract/exam_command_handler_schema_test.go": "完整审读：按对象字段边界、数组递归及原始类型拆分实际响应合同验证器；保留引用/枚举/可空处理和原错误位置。",
}, 5)
