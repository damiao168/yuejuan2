"""固化 full03 两项误报的只读语义和本机 runner 证据，不访问平台或浏览器。"""
import ast
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import subprocess
import tokenize
from io import StringIO

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent
CHECKPOINT = REPORTS / 'full-checkpoint-03'
CLI = Path('C:/Users/pc/AppData/Roaming/npm/node_modules/@playwright/cli')

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))

def main():
    scope = read(CHECKPOINT / 'source-scope.json')
    frozen_root = Path(scope['snapshot'])
    source_map = {entry['path']: entry for entry in scope['files']}
    files = ['docs/architecture/tools/build_preview.py',
             'docs/architecture/tools/check_preview_typography.js']
    captured = []
    for relative in files:
        source = ROOT / relative
        captured.append({'file': relative, 'full_file_read': True,
                         'current_sha256': digest(source),
                         'scan_scope_sha256': source_map[relative]['sha256'],
                         'actual_frozen_sha256': digest(frozen_root / relative),
                         'current_matches_scanned': digest(source) == source_map[relative]['sha256'],
                         'frozen_matches_recorded': digest(frozen_root / relative) == source_map[relative]['sha256'],
                         'line_count': len(source.read_text(encoding='utf-8').splitlines())})
    preview = (ROOT / files[0]).read_text(encoding='utf-8')
    tokens = list(tokenize.generate_tokens(StringIO(preview).readline))
    comments = [{'line': token.start[0], 'text': token.string} for token in tokens
                if token.type == tokenize.COMMENT and token.start[0] in [17, 18]]
    without_comments = tokenize.untokenize([token for token in tokens if token.type != tokenize.COMMENT])
    ast_identical = ast.dump(ast.parse(preview), include_attributes=False) == ast.dump(ast.parse(without_comments), include_attributes=False)
    bundle = CLI / 'node_modules/playwright-core/lib/coreBundle.js'
    lines = bundle.read_text(encoding='utf-8').splitlines()
    ranges = [(67013, 67071), (68416, 68428)]
    loader = [{'file': str(bundle), 'start_line': start, 'end_line': end,
               'source': '\n'.join(lines[start - 1:end])} for start, end in ranges]
    reference = CLI / 'skills/playwright-cli/references/running-code.md'
    # 只把函数表达式编译为值，完全不调用该函数，因此没有浏览器、HTTP或产物写入。
    probe = "const fs=require('fs'),vm=require('vm'); const input=process.argv[1]; const code=fs.readFileSync(input,'utf8'); const fn=vm.runInNewContext('('+code+')'); if(typeof fn!=='function'||fn.length!==1) process.exit(2); console.log(JSON.stringify({expression_value_type:typeof fn,argument_count:fn.length,constructor_name:fn.constructor.name,body_invoked:false,browser_started:false}));"
    completed = subprocess.run(['node', '-e', probe, str(ROOT / files[1])],
                               text=True, capture_output=True, encoding='utf-8')
    expression_validation = {'exit_code': completed.returncode, 'stdout': completed.stdout,
                             'stderr': completed.stderr, 'command': 'node -e [只读 vm 函数输入解析；函数体不调用] check_preview_typography.js'}
    if completed.returncode == 0:
        expression_validation['result'] = json.loads(completed.stdout)
    states = read(CHECKPOINT / 'pre-disposition-checkpoint02-new-statuses.json')['issues']
    keys = ['eb390af3-af14-4c27-b33c-0b1442f95181', '98688ff8-f75e-4bbb-897a-2eaf11ee1c8f']
    issues = [entry for entry in states if entry['key'] in keys]
    baseline = Path('D:/tools/sonar-remediation-20261011/root-new-findings-baseline')
    old_js = baseline / files[1]
    if old_js.is_file():
        before = old_js.read_text(encoding='utf-8')
        current = (ROOT / files[1]).read_text(encoding='utf-8')
        only_error_constructor_changes = before.replace('throw Error(', 'throw new Error(') == current
    else:
        only_error_constructor_changes = None
    result = {
        'audited_at_utc': datetime.now(timezone.utc).isoformat(),
        'reviewer': '/root/sonar_python_fix',
        'analysis_id': read(CHECKPOINT / 'compute-engine-task.json')['task']['analysisId'],
        'scope': '两份当前/冻结源码全文审读，逐 key 对照原始平台告警；本机已安装 CLI loader 只读审读和函数输入解析。不修改源码、生成产物、平台状态，不启动浏览器。',
        'source_files': captured,
        'issue_records': [{field: entry.get(field) for field in ['key', 'rule', 'component', 'line', 'issueStatus', 'message']} for entry in issues],
        'python_s125': {
            'key': keys[0], 'line_17_18_comment_tokens': comments,
            'ast_unchanged_if_comments_removed': ast_identical,
            'reviewed_code': '\n'.join(preview.splitlines()[14:23]),
            'reason': '第17–18行是解释离线机器字体回退及只修改内嵌副本边界的中文说明。实际替换 SVG font-family 的代码在第19行运行；说明并非被注释掉的 Python 语句。删除正确维护说明不能修复真实缺陷，保留并做平台 FALSE_POSITIVE 处置有具体源码依据。',
            'provenance': '本代理未修改 root 所有的 build_preview.py；保留已有 XML 安全解析等外部变更，不将其记为本代理修复。',
        },
        'javascript_s905': {
            'key': keys[1], 'reason': '该脚本是 playwright-cli run-code --filename 的单函数输入。CLI 将文件内容在括号内求值成 __fn__，再 await __fn__(page)，所以 async(page)=>表达式的值被运行器使用，不是普通 Node 模块的无用语句。自行改为 IIFE 会丢失 runner 注入的 page，违背该入口协议。',
            'documented_invocation': 'playwright-cli run-code --filename=docs/architecture/tools/check_preview_typography.js',
            'installed_cli_version': read(CLI / 'package.json')['version'],
            'installed_playwright_core_version': read(CLI / 'node_modules/playwright-core/package.json')['version'],
            'loader_source': loader,
            'reference_file': str(reference),
            'reference_lines_14_23': '\n'.join(reference.read_text(encoding='utf-8').splitlines()[13:23]),
            'filename_read_vm_parse_page_call_confirmed': True,
            'expression_validation': expression_validation,
            'baseline_only_error_constructor_changes': only_error_constructor_changes,
            'business_effects_read': 'page.goto 仅指向127.0.0.1:8766本地图纸；检查深链接、100%原生尺寸、换页150%缩放、fit和9页联系表；POST本地预览检查JSON并写一张截图。',
            'limitations': '本批仅解析函数输入并审读 loader，不调用函数体。本轮真实浏览器/字体/尺寸验收未执行；旧 preview-typography-validation.json 是历史证据，未回写到当前 SHA 或作为本轮 pass。',
        },
        'evidence_files': [{'file': str(path), 'sha256': digest(path)} for path in [
            bundle, CLI / 'package.json', CLI / 'node_modules/playwright-core/package.json',
            reference, CHECKPOINT / 'pre-disposition-checkpoint02-new-statuses.json',
            CHECKPOINT / 'source-scope.json']],
        'platform_action': 'review_recommends_false_positive_for_two_keys_only_root_may_execute',
        'passed': all(entry['current_matches_scanned'] and entry['frozen_matches_recorded'] for entry in captured)
                  and ast_identical and completed.returncode == 0 and len(issues) == 2,
    }
    (REPORTS / 'full03-fp-independent-review-python-agent.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    markdown = ['# full03 两项待处置误报：独立只读审读', '',
                f"analysisId `{result['analysis_id']}`。两份当前源/冻结源与 scope SHA 均匹配：{result['passed']}。", '',
                '`eb390af3-af14-4c27-b33c-0b1442f95181` / python:S125：build_preview.py 第17–18行是中文字体回退说明。tokenize 确认属于 COMMENT；删除注释不改变 AST，实际字体替换由第19行执行。符合保留维护规则说明的要求，建议平台 FALSE_POSITIVE。', '',
                '`98688ff8-f75e-4bbb-897a-2eaf11ee1c8f` / javascript:S905：check_preview_typography.js 是 `playwright-cli run-code --filename=...` 的函数输入。已安装 @playwright/cli 0.1.18 的 coreBundle.js 第67038–67062行读取文件、在括号内求值并 await __fn__(page)；第68416–68428行定义命令映射。函数值由 runner 实际消费，建议平台 FALSE_POSITIVE。', '',
                f"只读 Node vm 函数输入解析实际 exit {completed.returncode}；未调用函数体或启动浏览器，不能声称当前浏览器验收通过。详细 loader 原文、SHA、告警 key 和限制见同名 JSON。", '']
    (REPORTS / 'full03-fp-independent-review-python-agent.md').write_text('\n'.join(markdown), encoding='utf-8')
    # 保存当前 full03 未完成结论，后续最终审计独立覆盖另一个文件不会回写历史。
    for extension in ['json', 'md']:
        current = REPORTS / f'delivery-independent-final-platform-audit-python-agent.{extension}'
        archived = REPORTS / f'delivery-independent-checkpoint03-platform-audit-python-agent.{extension}'
        archived.write_bytes(current.read_bytes())
    print(json.dumps({'passed': result['passed'], 'sources': captured,
                      'expression_probe_exit': completed.returncode,
                      'baseline_only_error_constructor_changes': only_error_constructor_changes}, ensure_ascii=False))
    return 0 if result['passed'] else 1

if __name__ == '__main__':
    raise SystemExit(main())
