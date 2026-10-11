"""补记已经实际抽读的证据与交付快照；不把抽读升级为全文完成。"""
from pathlib import Path
from datetime import datetime, timezone
import hashlib
import json

ROOT = Path(__file__).resolve().parents[3]
BASE = ROOT / 'docs/architecture'
EVIDENCE = BASE / 'evidence'

# 前七包由独立复核实际抽读；后七包由主任务补读。此表不是自动推断职责的规则。
READ_RANGES = {
    'capture/handlers.go': [(91,115),(657,665),(726,772)],
    'exam/handlers.go': [(47,91),(165,214)],
    'modelgovernance/handlers.go': [(75,121),(483,517),(626,660)],
    'modelgovernance/managed_config_handlers.go': [(75,122)],
    'processing/service.go': [(28,85)],
    'processing/store_postgres.go': [(106,176)],
    'db/tenant_context.go': [(74,82),(104,122)],
    'auth/middleware.go': [(65,86)],
    'grading/engine.go': [(72,200)],
    'questionbank/handlers.go': [(309,328),(329,402),(477,489)],
    'releasegate/service.go': [(1,164)],
    'review/handlers.go': [(193,279),(706,729),(858,877)],
    'scorerelease/service.go': [(1,185)],
    'server/router_guards.go': [(1,135)],
    'subjective/handlers_grade.go': [(1,205)],
    'subjective/grade_settlement_pipeline.go': [(1,76)],
    'workerruntime/handlers.go': [(1,174)],
}


def read_json(name):
    return json.loads((EVIDENCE/name).read_text(encoding='utf-8-sig'))


def append_supplemental_source(modules, suffix, ranges):
    module = suffix.split('/')[0]
    path = 'services/api-gateway/internal/' + suffix
    data = (ROOT / path).read_bytes()
    sha = hashlib.sha256(data).hexdigest()
    count = len(data.decode('utf-8-sig').splitlines())
    if any(end > count for _, end in ranges):
        raise ValueError('抽读范围越界：' + path)
    sources = modules[module]['sources']
    source = next((item for item in sources if item['path'] == path), None)
    # 抽读证据只对应当时的版本；源码变化必须重新审读，不能替换 hash 后沿用旧证据。
    if source and source['sha256'] != sha:
        raise ValueError('已经抽读的源码发生变化：' + path)
    if source is None:
        source = {'path': path, 'sha256': sha, 'read_status': 'partial',
                  'full_file_review': False, 'ranges': [], 'symbols': []}
        sources.append(source)
    for start, end in ranges:
        scope = {'start': start, 'end': end}
        if scope not in source['ranges']:
            source['ranges'].append(scope)


def recheck_worker_sources(pipeline):
    # 两个变化后的 Worker 文件仅核对相关架构边界；依赖及运行行为仍未完整验证。
    worker_sources = {
        'services/ocr-worker/ocr_worker/config.py': '配置加载与校验分组；顺序OCR仅允许batch_size=1，数学服务默认HTTPS；未执行运行验收。',
        'services/math-verification-worker/math_verification_worker/server.py': '符号验证用spawn子进程和预算回收；TLS与显式开发回环例外；未执行服务或数学效果验收。',
    }
    for path, reason in worker_sources.items():
        entry = next((item for item in pipeline['files'] if item['file'] == path), None)
        if entry is None:
            entry = {'file': path}
            pipeline['files'].append(entry)
        entry.update(source_sha256=hashlib.sha256((ROOT / path).read_bytes()).hexdigest(),
                     read_status='partial', analysis_status='pending', status='pending', modified=False, reason=reason)
    pipeline['final_worker_change_recheck_at_utc'] = datetime.now(timezone.utc).isoformat()
    pipeline['counts']['partial'] = sum(item.get('read_status') == 'partial' for item in pipeline['files'])
    pipeline['counts']['reviewed'] = sum(item.get('read_status') == 'complete' for item in pipeline['files'])
    pipeline['counts']['pending'] = len(pipeline['files']) - pipeline['counts']['partial'] - pipeline['counts']['reviewed']


def main():
    manifest = read_json('module-responsibilities.json')
    modules = {entry['module']: entry for entry in manifest['modules']}
    for suffix, ranges in READ_RANGES.items():
        append_supplemental_source(modules, suffix, ranges)
    manifest['supplemental_review'] = '14复杂包补核核心函数体；独立复核与主任务实际读取，仍为partial。'
    manifest['supplemental_checked_at_utc'] = datetime.now(timezone.utc).isoformat()
    (EVIDENCE / 'module-responsibilities.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
    pipeline = read_json('pipeline-reviewed-files.json')
    recheck_worker_sources(pipeline)
    (EVIDENCE / 'pipeline-reviewed-files.json').write_text(json.dumps(pipeline, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'module_source_files': len({s['path'] for m in manifest['modules'] for s in m['sources']}),
                      'worker_changed_files_rechecked': 2}))


if __name__=='__main__':
    main()
