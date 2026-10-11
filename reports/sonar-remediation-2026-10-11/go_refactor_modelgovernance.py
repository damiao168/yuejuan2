"""按已审读的协议阶段抽取测试；保留原案例、断言和失败顺序。只编辑正式交接的六文件。"""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
DIRECTORY = 'services/api-gateway/internal/modelgovernance/'


def masked(source):
    # 只用来找到 Go 代码块边界；忽略字符串、原始字符串、字符与注释中的花括号。
    result = list(source)
    pattern = re.compile(r'"(?:\\.|[^"\\])*"|`[^`]*`|\'(?:\\.|[^\'\\])*\'|//[^\n]*|/\*[\s\S]*?\*/')
    for match in pattern.finditer(source):
        for position in range(match.start(), match.end()):
            if source[position] != '\n':
                result[position] = ' '
    return ''.join(result)


def block(source, start):
    clean = masked(source)
    depth = 0
    for position in range(start, len(source)):
        if clean[position] == '{':
            depth += 1
        elif clean[position] == '}':
            depth -= 1
            if depth == 0:
                return position
    raise ValueError('unbalanced Go block')


def function(source, name):
    match = re.search(r'^func ' + re.escape(name) + r'\([^\n]*\) \{', source, re.M)
    if not match:
        raise ValueError(name)
    return match.start(), block(source, match.end() - 1) + 1


def dedent(body, amount):
    prefix = '\t' * amount
    return '\n'.join(line[amount:] if line.startswith(prefix) else line for line in body.split('\n'))


def named_case(source, name):
    position = source.index('[]struct {')
    start = source.index('{', position)
    end = block(source, start)
    definition = 'type ' + name + ' struct {' + dedent(source[start + 1:end], 1) + '}\n\n'
    return source[:position] + '[]' + name + source[end + 1:], definition


def closure_body(source, marker):
    position = source.index(marker)
    start = position + len(marker) - 1
    end = block(source, start)
    return start, end, source[start + 1:end]


def case_helper(source, marker, helper_name, parameters, arguments, comment):
    start, end, body = closure_body(source, marker)
    helper = comment + '\nfunc ' + helper_name + '(' + parameters + ') {\n\tt.Helper()' + dedent(body, 2) + '}\n\n'
    changed = source[:start + 1] + '\n\t\t\t' + helper_name + '(' + arguments + ')\n\t\t' + source[end:]
    return changed, helper


def callback_helper(case_source, helper_name, signature, arguments, comment):
    marker = 'server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {'
    start, end, body = closure_body(case_source, marker)
    helper = comment + '\nfunc ' + helper_name + '(' + signature + ') {\n\tt.Helper()' + dedent(body, 1) + '}\n\n'
    changed = case_source[:start + 1] + '\n\t\t' + helper_name + '(' + arguments + ')\n\t' + case_source[end:]
    return changed, helper


def fatal_inventory(source):
    # 验证原测试的失败/日志调用没有被删掉。仅是保真辅助，不替代实际运行测试。
    output = []
    clean = masked(source)
    for match in re.finditer(r'\bt\.(Fatalf|Fatal|Errorf|Error|Logf)\(', clean):
        depth, position = 1, match.end()
        while depth:
            if clean[position] == '(':
                depth += 1
            elif clean[position] == ')':
                depth -= 1
            position += 1
        output.append(re.sub(r'\s+', ' ', source[match.start():position]).strip())
    return Counter(output)


def native_round_trip(source):
    name = 'TestHTTPAgentNativeToolRoundTripForBothAdapters'
    begin, finish = function(source, name)
    original = source[begin:finish]
    changed, exercise = case_helper(original, 't.Run(adapter, func(t *testing.T) {', 'exerciseAgentNativeRoundTrip',
                                    't *testing.T, adapter string', 't, adapter',
                                    '// 每个适配器独立完成规划、工具结果回传和最终回答；不能共享请求计数或可信工具历史。')
    marker = 'server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {'
    start, end, body = closure_body(exercise, marker)
    body = dedent(body, 1)
    tools = body.index('\tif string(parameters["max_tokens"])')
    choice = body.index('\tvar wireMessages []agentProviderMessage')
    response = body.index('\tw.Header().Set("Content-Type"')
    payload_body = body[:tools].replace('\n\t\treturn\n', '\n\t\treturn nil, nil, false\n', 1)
    helpers = ('// 先校验凭证与适配器信封，再把同一原生工具参数交给后续预算和历史检查。\n'
               'func readAgentRoundTripPayload(t *testing.T, r *http.Request, adapter string) (map[string]json.RawMessage, json.RawMessage, bool) {\n\tt.Helper()'
               + payload_body + '\treturn parameters, messages, true\n}\n\n'
               'func verifyAgentRoundTripTools(t *testing.T, parameters map[string]json.RawMessage) {\n\tt.Helper()\n'
               + body[tools:choice] + '}\n\n'
               '// 请求计数在历史解码后推进，首轮规划和工具结果轮的检查顺序与原测试一致。\n'
               'func agentRoundTripChoice(t *testing.T, parameters map[string]json.RawMessage, messages json.RawMessage, requests *atomic.Int32) string {\n\tt.Helper()\n'
               + body[choice:response] + '\treturn choice\n}\n\n'
               'func writeAgentRoundTripResponse(w http.ResponseWriter, adapter, choice string) {\n'
               + body[response:] + '}\n\n')
    replacement = ('\n\t\tparameters, messages, ok := readAgentRoundTripPayload(t, r, adapter)\n'
                   '\t\tif !ok {\n\t\t\treturn\n\t\t}\n'
                   '\t\tverifyAgentRoundTripTools(t, parameters)\n'
                   '\t\tchoice := agentRoundTripChoice(t, parameters, messages, &requests)\n'
                   '\t\twriteAgentRoundTripResponse(w, adapter, choice)\n\t')
    exercise = exercise[:start + 1] + replacement + exercise[end:]
    return source[:begin] + helpers + exercise + changed + source[finish:]


def final_stream(source):
    name = 'TestHTTPAgentFinalStreamForBothAdapters'
    begin, finish = function(source, name)
    changed, exercise = case_helper(source[begin:finish], 't.Run(adapter, func(t *testing.T) {', 'exerciseAgentFinalStream',
                                    't *testing.T, adapter string', 't, adapter',
                                    '// 最终回答阶段关闭原生工具调用，分别验证两种协议的增量事件和完整用量。')
    exercise, handler = callback_helper(exercise, 'writeAgentFinalStream',
                                        't *testing.T, adapter string, w http.ResponseWriter, r *http.Request', 't, adapter, w, r',
                                        '// 原生与兼容协议使用各自的流开关和结束信封，保持事件发送顺序。')
    return source[:begin] + handler + exercise + changed + source[finish:]


def thinking_fields(source):
    name = 'TestHTTPAgentAppliesOnlyKnownThinkingCompatibilityFields'
    begin, finish = function(source, name)
    current, definition = named_case(source[begin:finish], 'agentThinkingTestCase')
    changed, exercise = case_helper(current, 't.Run(test.name, func(t *testing.T) {', 'exerciseAgentThinkingFields',
                                    't *testing.T, test agentThinkingTestCase', 't, test',
                                    '// 仅已知混合思考模型收到私有兼容参数，其他供应商和专用推理模型必须保持原请求形状。')
    exercise, handler = callback_helper(exercise, 'writeAgentThinkingResponse',
                                        't *testing.T, test agentThinkingTestCase, w http.ResponseWriter, r *http.Request', 't, test, w, r',
                                        '// 直接检查线上编码后的字段，避免测试只验证内存配置而遗漏协议适配。')
    return source[:begin] + definition + handler + exercise + changed + source[finish:]


def timeout_case(source):
    name = 'TestHTTPManagedAPIProberBoundsGenerationWithoutRetry'
    begin, finish = function(source, name)
    changed, exercise = case_helper(source[begin:finish], 't.Run(name, func(t *testing.T) {', 'exerciseManagedGenerationTimeout',
                                    't *testing.T, flushHeaders bool', 't, flushHeaders',
                                    '// 同一能力预算分别在等待响应头和等待正文时耗尽；超时不能重试生成，也不能耗尽调用方预算。')
    return source[:begin] + exercise + changed + source[finish:]


def compatible_capability(source):
    name = 'TestHTTPManagedAPIProberChecksOpenAICompatibleModelAndStructuredOutput'
    begin, finish = function(source, name)
    current = source[begin:finish]
    marker = 'server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {'
    start, end, body = closure_body(current, marker)
    body = dedent(body, 1).replace('\t\tmodelRequests++', '\t\t(*modelRequests)++').replace('\t\tcompletionRequests++', '\t\t(*completionRequests)++')
    helper = ('// 先发现可用模型再发送有界JSON能力请求，两个阶段分别计数且使用同一测试凭证。\n'
              'func writeManagedCompatibleCapability(t *testing.T, w http.ResponseWriter, r *http.Request, modelRequests, completionRequests *int) {\n\tt.Helper()'
              + body + '}\n\n')
    current = current[:start + 1] + '\n\t\twriteManagedCompatibleCapability(t, w, r, &modelRequests, &completionRequests)\n\t' + current[end:]
    return source[:begin] + helper + current + source[finish:]


def typed_case(source, test_name, case_type, helper_name, comment, extra_signature='', extra_arguments=''):
    begin, finish = function(source, test_name)
    current, definition = named_case(source[begin:finish], case_type)
    changed, exercise = case_helper(current, 't.Run(test.name, func(t *testing.T) {', helper_name,
                                    't *testing.T, test ' + case_type + extra_signature,
                                    't, test' + extra_arguments, comment)
    return source[:begin] + definition + exercise + changed + source[finish:]


def update_case(source):
    source = typed_case(source, 'TestBatch6ManagedUpdateProbesBeforeSaving', 'batch6ManagedUpdateCase',
                        'exerciseBatch6ManagedUpdate',
                        '// 默认模型更换必须先完整能力探测，备用模型仅快速探测；失败后旧配置和密钥必须保留。')
    begin, finish = function(source, 'exerciseBatch6ManagedUpdate')
    exercise = source[begin:finish]
    start = exercise.index('\tif response.Code != test.wantStatus')
    end = block(exercise, exercise.index('{', start)) + 1
    condition = exercise[start:end]
    helper = ('// 检查探测模式、调用次数和被探测的新连接，随后才读取最终存储状态。\n'
              'func verifyBatch6ManagedUpdateProbe(t *testing.T, response *httptest.ResponseRecorder, prober *batch6UpdateProber, test batch6ManagedUpdateCase, update ManagedAPIConfigUpdateInput) {\n\tt.Helper()\n'
              + condition + '\n}\n\n')
    exercise = exercise[:start] + '\tverifyBatch6ManagedUpdateProbe(t, response, prober, test, update)' + exercise[end:]
    return source[:begin] + helper + exercise + source[finish:]


def role_cases(source):
    name = 'TestResolvePanelRoleBindingsRequiresCompleteGovernedRolesAndIndependentArbiter'
    begin, finish = function(source, name)
    current = source[begin:finish]
    start, end, inputs = closure_body(current, 'newInputs := func() []SaveModelRoleBindingInput {')
    declaration_start = current.rfind('\tnewInputs', 0, start)
    current = current[:declaration_start] + current[end + 1:]
    input_helper = 'func panelRoleFailureInputs() []SaveModelRoleBindingInput {' + dedent(inputs, 1) + '}\n\n'
    current, definition = named_case(current, 'panelRoleFailureCase')
    changed, exercise = case_helper(current, 't.Run(tt.name, func(t *testing.T) {', 'exercisePanelRoleFailure',
                                    't *testing.T, tt panelRoleFailureCase', 't, tt',
                                    '// 每个案例单独配置三角色，保护缺席、模型复用、强度不足与未验证模型的拒绝边界。')
    exercise = exercise.replace('inputs := newInputs()', 'inputs := panelRoleFailureInputs()')
    start = exercise.index('\tfor i := range inputs {')
    brace = exercise.index('{', start)
    end = block(exercise, brace)
    loop_body = exercise[brace + 1:end]
    switch_body = dedent(loop_body, 1).replace('inputs[i]', 'input')
    mutation_helper = ('func configurePanelRoleFailureInput(input *SaveModelRoleBindingInput, tt panelRoleFailureCase) {'
                       + switch_body + '}\n\n')
    exercise = exercise[:brace + 1] + '\n\t\tconfigurePanelRoleFailureInput(&inputs[i], tt)\n\t' + exercise[end:]
    return source[:begin] + definition + input_helper + mutation_helper + exercise + changed + source[finish:]


def sandbox_case(source):
    name = 'TestSandboxApprovalRefactorRequiresRevocationEvenAfterExpiry'
    begin, finish = function(source, name)
    changed, exercise = case_helper(source[begin:finish], 't.Run(map[bool]string{false: "legacy", true: "managed"}[managed], func(t *testing.T) {',
                                    'exerciseExpiredSandboxApproval', 't *testing.T, managed bool', 't, managed',
                                    '// 过期审批仍占用重复创建身份，必须显式撤销后再建；旧记录不能被失败的创建改写。')
    return source[:begin] + exercise + changed + source[finish:]


def privacy_audit(source):
    name = 'TestManagedChatUsesTenantDefaultWithoutExposingCredential'
    begin, finish = function(source, name)
    current = source[begin:finish]
    start = current.index('\trecords, err := audits.ListAudits')
    body = current[start:-1]
    helper = ('// 审计只记录治理元数据；对话正文和供应商密钥不能经reason或AfterValue泄露。\n'
              'func verifyManagedChatPrivacyAudit(t *testing.T, audits *auth.MemoryStore, user auth.User) {\n\tt.Helper()\n'
              + body + '}\n\n')
    current = current[:start] + '\tverifyManagedChatPrivacyAudit(t, audits, user)\n}'
    return source[:begin] + helper + current + source[finish:]


def main():
    handoff = json.loads((REPORT / 'go-modelgovernance-handoff.json').read_text('utf-8'))
    names = sorted({item['path'] for item in handoff['issues']})
    originals = {name: (ROOT / name).read_text('utf-8-sig') for name in names}
    transformations = {
        'governance_routing_state_refactor_test.go': [sandbox_case],
        'managed_agent_http_test.go': [native_round_trip, final_stream, thinking_fields],
        'managed_chat_test.go': [privacy_audit],
        'managed_config_probe_http_test.go': [timeout_case, compatible_capability,
            lambda text: typed_case(text, 'TestHTTPManagedAPIProberRejectsCredentialAndUnknownModelBeforeGeneration', 'managedCredentialFailureCase',
                                    'exerciseManagedCredentialFailure', '// 凭证或模型发现失败必须在计费生成前退出，同时保留首次检查的错误归因。')],
        'managed_governance_refactor_test.go': [
            lambda text: typed_case(text, 'TestBatch6ManagedDeleteDependencyOrderAndAtomicity', 'batch6ManagedDeleteCase',
                                    'exerciseBatch6ManagedDelete', '// 删除按角色、审批和策略检查依赖；失败保留配置与密钥，成功才做软删除并移除秘密。'),
            update_case,
            lambda text: typed_case(text, 'TestBatch6ManagedStreamReaderOrdering', 'batch6ManagedStreamCase',
                                    'exerciseBatch6ManagedStream', '// 事件按reasoning、content、done顺序消费；回调或读取失败按原位置停止，不能补发完成事件。',
                                    ', connection ManagedAPIConnection', ', connection')],
        'role_binding_test.go': [role_cases],
    }
    results = {}
    for name, original in originals.items():
        current = original
        for transform in transformations[Path(name).name]:
            current = transform(current)
        assert fatal_inventory(original) == fatal_inventory(current), ('failure/log inventory changed', name)
        assert re.findall(r'^func (Test\w+)\(', original, re.M) == re.findall(r'^func (Test\w+)\(', current, re.M), ('test identity changed', name)
        results[name] = current
    # 所有变换和静态保真条件先成立，再落盘；写入前核对无人改动交接范围。
    for name in names:
        assert (ROOT / name).read_text('utf-8-sig') == originals[name], ('concurrent edit', name)
    files = []
    for name, current in results.items():
        path = ROOT / name
        raw = path.read_bytes()
        before = hashlib.sha256(raw).hexdigest()
        encoding_prefix = b'\xef\xbb\xbf' if raw.startswith(b'\xef\xbb\xbf') else b''
        newline = '\r\n' if b'\r\n' in raw else '\n'
        path.write_bytes(encoding_prefix + current.replace('\n', newline).encode('utf-8'))
        files.append({'path': name, 'before_sha256': before, 'current_sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
                      'full_file_review_status': 'reviewed', 'reviewed_lines_before': len(originals[name].splitlines()),
                      'original_test_identities_preserved': True, 'failure_and_log_call_inventory_preserved': True,
                      'failure_and_log_call_count': sum(fatal_inventory(current).values()), 'tests': 'pending_resource_coordination'})
    issues = []
    for issue in handoff['issues']:
        current = dict(issue)
        current.update(state='code_modified_pending_tests_and_platform_rescan', batch='modelgovernance-critical-01',
                       change='按协议编码/请求生命周期/错误原子性/流事件阶段抽取具名测试辅助函数，保留原测试案例与所有失败/日志调用',
                       current_sha256=next(item['current_sha256'] for item in files if item['path'] == issue['path']))
        issues.append(current)
    report = {'updated_at_utc': datetime.now(timezone.utc).isoformat(), 'original_count': len(issues), 'files_count': len(files),
              'issues': issues, 'files': files, 'platform_actions_performed': False,
              'runtime_validation': 'pending_scan_resource_coordination',
              'static_verification_limit': '失败/日志调用多重集及原Test身份保留仅证明未删断言，不替代实际测试、执行次序审读或平台复扫'}
    (REPORT / 'go-modelgovernance-progress.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({'modified_issues': len(issues), 'files': len(files)}))


if __name__ == '__main__':
    main()
