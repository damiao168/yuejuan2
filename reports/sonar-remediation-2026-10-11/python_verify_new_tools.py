"""比较新增告警工具的冻结当前基线与修复版，生成写入只存在于内存。"""
from datetime import datetime, timezone
import hashlib
import io
import json
from pathlib import Path
import contextlib

import python_verify_tools as verifier

ROOT, REPORT = verifier.ROOT, verifier.REPORT
verifier.BACKUP = Path('D:/tools/sonar-remediation-20261011/python-new-baseline')
NAMES = ['finalize_delivery_records', 'fit_box_typography', 'retire_stale_reviews',
         'write_frontend_specs', 'verify_typography']


def normalize_clock(value, fields):
    if isinstance(value, dict):
        return {key: normalize_clock(item, fields) for key, item in value.items() if key not in fields}
    if isinstance(value, list):
        return [normalize_clock(item, fields) for item in value]
    return value


def run(name, current, ranges_override=None):
    stream = io.StringIO()
    with verifier.VirtualFiles() as files, contextlib.redirect_stdout(stream):
        try:
            module = verifier.load_module(name, current)
            if ranges_override is not None:
                module.READ_RANGES = ranges_override
            if name == 'write_frontend_specs':
                features, apis = module.feature_pages(), module.api_pages()
                result = {'missing': module.validate_paths(features + apis)}
                module.save('01-feature-tree.drawio', '完整系统功能树', features)
                module.save('08-api-data-flow.drawio', '核心 API 与数据流', apis)
            else:
                result = module.main()
            error = None
        except Exception as exc:
            result = None
            error = {'type': type(exc).__name__, 'message': str(exc)}
    writes = {}
    for path, data in files.writes.items():
        fields = set()
        if name == 'finalize_delivery_records':
            fields = {'supplemental_checked_at_utc', 'final_worker_change_recheck_at_utc'}
        elif name == 'retire_stale_reviews':
            fields = {'invalidated_at_utc'}
        elif name == 'verify_typography':
            fields = {'checked_at_utc'}
        if fields:
            data = json.dumps(normalize_clock(json.loads(data), fields), ensure_ascii=False).encode('utf-8')
        writes[path] = {'sha256': hashlib.sha256(data).hexdigest(), 'bytes': len(data)}
    return {'return_value': result, 'error': error, 'stdout': stream.getvalue(), 'writes': writes}


def supplemental_fixture():
    # 更新只发生在内存输入：构造版本匹配的抽读记录，验证正常补记路径；不替真实源码续认审读。
    module = verifier.load_module('finalize_delivery_records', True)
    path = ROOT / 'docs/architecture/evidence/module-responsibilities.json'
    with verifier.REAL_OPEN(path, 'rb') as stream:
        value = json.load(stream)
    modules = {entry['module']: entry for entry in value['modules']}
    for suffix in module.READ_RANGES:
        relative = 'services/api-gateway/internal/' + suffix
        actual = hashlib.sha256((ROOT / relative).read_bytes()).hexdigest()
        sources = modules[suffix.split('/')[0]]['sources']
        for source in sources:
            if source['path'] == relative:
                source['sha256'] = actual
    verifier.READ_CACHE[str(path.resolve())] = json.dumps(value, ensure_ascii=False).encode('utf-8')


def main():
    hashes = {verifier.TOOL_DIR + name + '.py': hashlib.sha256((ROOT / (verifier.TOOL_DIR + name + '.py')).read_bytes()).hexdigest()
              for name in NAMES}
    records = []
    for name in NAMES:
        baseline, current = run(name, False), run(name, True)
        records.append({'tool': name, 'input_mode': 'actual_current_cached_input', 'equivalent': baseline == current,
                        'baseline': baseline, 'current': current})
        print(name, 'PASS' if baseline == current else 'FAIL', 'writes', len(current['writes']), 'error', current['error'], flush=True)
    supplemental_fixture()
    name = 'finalize_delivery_records'
    baseline, current = run(name, False), run(name, True)
    records.append({'tool': name, 'input_mode': 'in_memory_version_matching_supplemental_fixture',
                    'equivalent': baseline == current, 'baseline': baseline, 'current': current})
    print(name, 'matching version fixture', 'PASS' if baseline == current else 'FAIL', 'writes', len(current['writes']), 'error', current['error'], flush=True)
    input_path = str((ROOT / 'docs/architecture/evidence/module-responsibilities.json').resolve())
    matching_input = verifier.READ_CACHE[input_path]
    for mode in ('stale_source_guard', 'out_of_range_guard', 'new_partial_source'):
        value = json.loads(matching_input)
        capture = next(item for item in value['modules'] if item['module'] == 'capture')
        source = next(item for item in capture['sources'] if item['path'] == 'services/api-gateway/internal/capture/handlers.go')
        if mode == 'stale_source_guard':
            source['sha256'] = '0' * 64
        elif mode == 'new_partial_source':
            capture['sources'].remove(source)
        verifier.READ_CACHE[input_path] = json.dumps(value, ensure_ascii=False).encode('utf-8')
        override = {'capture/handlers.go': [(1, 1000000)]} if mode == 'out_of_range_guard' else None
        baseline, current = run(name, False, override), run(name, True, override)
        expected = (current['error'] is not None and current['error']['type'] == 'ValueError' and not current['writes']) if mode.endswith('_guard') else (current['error'] is None and len(current['writes']) == 2)
        records.append({'tool': name, 'input_mode': mode, 'equivalent': baseline == current,
                        'expected_boundary_observed': expected, 'baseline': baseline, 'current': current})
        print(name, mode, 'PASS' if baseline == current and expected else 'FAIL', flush=True)
    verifier.READ_CACHE[input_path] = matching_input
    # 同一工具还涉及原告警，补验原冻结基线，避免新增批次让原账本绑定旧版本。
    new_backup = verifier.BACKUP
    verifier.BACKUP = Path('D:/tools/sonar-remediation-20261011/baseline')
    baseline = run('write_frontend_specs', False)
    verifier.BACKUP = new_backup
    current = run('write_frontend_specs', True)
    records.append({'tool': 'write_frontend_specs', 'input_mode': 'original_190_issue_baseline',
                    'equivalent': baseline == current, 'baseline': baseline, 'current': current})
    report = {'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'records': records,
              'current_source_hashes': hashes, 'sources_unchanged': all(hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == digest for path, digest in hashes.items()),
              'writes_to_workspace': False, 'quality_gate_claim': False,
              'clock_fields_excluded_only': ['supplemental_checked_at_utc', 'final_worker_change_recheck_at_utc', 'invalidated_at_utc', 'checked_at_utc'],
              'comparison_scope': '同一缓存本地输入；保持真实拒绝旧审读版本行为；另用内存版本匹配夹具覆盖正常补记。输出全部字段与字节哈希比较，只有列出的实时钟字段剔除。',
              'input_files': [{'file': path, 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()} for path, data in sorted(verifier.READ_CACHE.items())]}
    report['passed'] = all(item['equivalent'] and item.get('expected_boundary_observed', True) for item in records) and report['sources_unchanged']
    (REPORT / 'python-new-tools-equivalence.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
