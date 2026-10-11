"""撤销已变化源码的旧全文完成声明；保留旧证据，不代替重新审读。"""
from datetime import datetime, timezone
from pathlib import Path
import hashlib
import json

ROOT = Path(__file__).resolve().parents[3]
EVIDENCE = ROOT / 'docs/architecture/evidence'


def retire_completed_review(entry, manifest_path):
    if entry.get('read_status') != 'complete':
        return None
    source = ROOT / entry.get('file', entry.get('path', ''))
    actual = hashlib.sha256(source.read_bytes()).hexdigest() if source.is_file() else None
    if actual == entry.get('source_sha256'):
        return None
    # 原 hash 和读取范围仍指向旧版本；只撤销完成资格，不能用新 hash 续认旧审读。
    entry['superseded_completed_review'] = {
        'source_sha256': entry.get('source_sha256'),
        'read_status': 'complete',
        'analysis_status': entry.get('analysis_status'),
        'reason': entry.get('reason', entry.get('scope', '')),
        'read_ranges': entry.get('read_ranges'),
    }
    entry.update(read_status='not_manually_reviewed', analysis_status='pending', status='pending',
                 reason='原全文完成版本的源码已变化；当前版本必须重新审读，不继承旧完成声明。',
                 invalidated_at_utc=datetime.now(timezone.utc).isoformat())
    return {'manifest': manifest_path.name, 'file': entry.get('file', entry.get('path'))}


def main():
    retired = []
    for path in sorted(EVIDENCE.glob('*-reviewed-files.json')):
        manifest = json.loads(path.read_text(encoding='utf-8-sig'))
        entries = manifest.get('files', []) if isinstance(manifest, dict) else manifest
        changed = False
        for entry in entries:
            record = retire_completed_review(entry, path)
            if record is not None:
                retired.append(record)
                changed = True
        if changed:
            path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'retired_completion_claims': retired}, ensure_ascii=False))


if __name__ == '__main__':
    main()
