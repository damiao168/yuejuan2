"""已审读的导入命令契约将同一请求的参数组成语义输入，不更改命令序列。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
path = Path("services/api-gateway/internal/apicontract/reliability_handler_schema_test.go")
source = path.read_text(encoding="utf-8")
start = source.index("func TestImportAcceptedCommandHandlerResponsesValidateAgainstOpenAPI(")
end = source.index("\nfunc TestReliabilitySharedClientContract(", start)
body = source[start:end]
body = body.replace("submit := func(route, method, idKey, id string, body any, status string, code int, handle http.HandlerFunc)", "submit := func(command importAcceptedContractCommand)")
for old, new in [
    ("json.Marshal(body)", "json.Marshal(command.body)"),
    ('httptest.NewRequest(method,', 'httptest.NewRequest(command.method,'),
    ("request.SetPathValue(idKey, id)", "request.SetPathValue(command.idKey, command.id)"),
    ('"contract-"+method+"-"+status', '"contract-"+command.method+"-"+command.status'),
    ("handle(response, request)", "command.handle(response, request)"),
    ("response.Code != code", "response.Code != command.code"),
    ('route, response.Code', 'command.route, response.Code'),
    ('document, route, strings.ToLower(method), status', 'document, command.route, strings.ToLower(command.method), command.status'),
]:
    assert old in body, old
    body = body.replace(old, new)
lines = body.splitlines()
calls = 0
for index, line in enumerate(lines):
    call = line.find('submit("')
    if call == -1:
        continue
    assert line.endswith(")"), line
    lines[index] = line[:call] + "submit(importAcceptedContractCommand{" + line[call + len("submit("):-1] + "})"
    calls += 1
assert calls == 7, calls
source = source[:start] + "\n".join(lines) + "\n" + source[end:]
source += '''
// 同一命令的路由、路径绑定、请求与期望状态放在一起，避免复用调用中交换参数；请求时序仍由测试顺序决定。
type importAcceptedContractCommand struct {
    route, method, idKey, id string
    body any
    status string
    code int
    handle http.HandlerFunc
}
'''
path.write_text(source, encoding="utf-8")
spec = importlib.util.spec_from_file_location("progress", root / "go-record-progress.py")
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch26", {str(path).replace("\\", "/"): "8 参数导入命令闭包改为语义请求结构；7处调用保留逐字段原值、幂等键、OpenAPI 期望、资源绑定与原顺序。"}, 1)
