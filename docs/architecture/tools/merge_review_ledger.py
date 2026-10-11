"""合并审读账本；自动枚举和受保护处理均不能替代有版本依据的人工审读。"""
from pathlib import Path
from collections import Counter, defaultdict
import csv
import hashlib
import json

ROOT = Path(__file__).resolve().parents[3]
EVIDENCE = ROOT / 'docs/architecture/evidence'


def source_hash(path):
    """只计算指纹，不把配置或凭据正文写入文档。"""
    if not path.is_file():
        return None
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def inventory_entries(value):
    return value.get('files', []) if isinstance(value, dict) else value


def hash_status(expected, actual):
    if actual is None:
        return 'missing_file'
    if not expected:
        return 'hash_missing'
    return 'match' if expected == actual else 'source_changed'


def append_review_entries(entries, manifest_source, automatic, inventory_paths, current_hashes, records, outside_inventory, rejected):
    for entry in entries:
        path = entry.get('file', entry.get('path'))
        if not path:
            raise ValueError('审读记录缺少路径: ' + manifest_source)
        if path not in inventory_paths:
            outside_inventory.append({'file': path, 'source': manifest_source})
            continue
        status = hash_status(entry.get('source_sha256'), current_hashes[path])
        requested_complete = entry.get('read_status', 'not_manually_reviewed') == 'complete'
        qualified = requested_complete and status == 'match' and not automatic
        # 保留分工原声明；版本不符或自动枚举的完成声明不能升级汇总状态。
        records[path].append({**entry, 'source': manifest_source, 'hash_status': status,
                              'qualified_complete': qualified, 'automatic_source': automatic})
        if requested_complete and not qualified:
            rejected.append({'file': path, 'source': manifest_source,
                             'reason': 'automatic_source' if automatic else status})


def effective_read_status(evidence):
    if any(record['qualified_complete'] for record in evidence):
        return 'complete'
    partial = any(record.get('read_status') == 'partial' and record['hash_status'] == 'match'
                  and not record['automatic_source'] for record in evidence)
    return 'partial' if partial else 'not_manually_reviewed'


def review_row(item, evidence, current_hash):
    read_status = effective_read_status(evidence)
    handling = item.get('handling', 'read_only')
    if any(record.get('handling') == 'protected' or record.get('status') == 'protected' for record in evidence):
        handling = 'protected'
    return {
        'path': item.get('path', item.get('file')),
        'review_status': 'reviewed' if read_status == 'complete' else 'pending',
        'read_status': read_status,
        'handling': handling,
        'source_sha256': current_hash or '',
        'review_sources': '; '.join(sorted({record['source'] for record in evidence})),
        'reason': '；'.join(dict.fromkeys([item.get('note', '')] + [record.get('reason', '') for record in evidence])),
        'review_records': json.dumps(evidence, ensure_ascii=False, separators=(',', ':')),
    }


def write_review_rows(rows):
    columns = ['path', 'review_status', 'read_status', 'handling', 'source_sha256',
               'review_sources', 'reason', 'review_records']
    with (EVIDENCE / 'file-review.tsv').open('w', encoding='utf-8', newline='') as stream:
        writer = csv.DictWriter(stream, columns, delimiter='\t')
        writer.writeheader()
        writer.writerows(rows)


def main():
    inventory_file = EVIDENCE / 'file-inventory.json'
    inventory = inventory_entries(json.loads(inventory_file.read_text('utf-8-sig')))
    paths = [entry.get('path', entry.get('file')) for entry in inventory]
    if len(paths) != len(set(paths)) or any(not path for path in paths):
        raise ValueError('原始盘点路径必须非空且不重复')
    inventory_paths = set(paths)
    records = defaultdict(list)
    source_manifests, outside_inventory, rejected = [], [], []
    current_hashes = {path: source_hash(ROOT / path) for path in paths}
    # 缺少分工记录时仍保留 pending；再次合并只能纳入实际落盘且版本有效的记录。
    for manifest_file in sorted(EVIDENCE.glob('*-reviewed-files.json')):
        value = json.loads(manifest_file.read_text('utf-8-sig'))
        entries = inventory_entries(value)
        manifest_source = manifest_file.relative_to(ROOT).as_posix()
        source_manifests.append({'file': manifest_source, 'sha256': source_hash(manifest_file), 'records': len(entries)})
        automatic = isinstance(value, dict) and value.get('automatic_enumeration_is_review') is True
        append_review_entries(entries, manifest_source, automatic, inventory_paths, current_hashes,
                              records, outside_inventory, rejected)
    rows = [review_row(item, records[item.get('path', item.get('file'))],
                       current_hashes[item.get('path', item.get('file'))]) for item in inventory]
    write_review_rows(rows)
    directory_counts = defaultdict(Counter)
    for row in rows:
        parts = row['path'].split('/')
        group = '/'.join(parts[:2]) if len(parts) > 1 else '(root files)'
        directory_counts[group][row['review_status']] += 1
    summary = {
        'method': '原始逐文件清单 + 各分工人工审读记录；read_status=complete且源SHA256匹配才计入完成',
        'automatic_scan_counts_as_review': False,
        'protected_counts_as_review': False,
        'inventory_source': {'file': inventory_file.relative_to(ROOT).as_posix(), 'sha256': source_hash(inventory_file)},
        'original_files': len(rows),
        'reviewed': sum(row['review_status'] == 'reviewed' for row in rows),
        'pending': sum(row['review_status'] == 'pending' for row in rows),
        'read_status_counts': dict(Counter(row['read_status'] for row in rows)),
        'handling_counts': dict(Counter(row['handling'] for row in rows)),
        'source_manifests': source_manifests,
        'directory_counts': {key: dict(value) for key, value in sorted(directory_counts.items())},
        'rejected_completion_claims': rejected,
        'records_outside_original_inventory': outside_inventory,
        'limitations': ['账本只汇总有版本依据的审读声明，不自行证明审读质量',
                        '尚未落盘的分工记录不会计入；交付前必须重新合并',
                        '原始文件清单固定；本次新文档不冒充原始源码审读覆盖'],
    }
    (EVIDENCE / 'audit-coverage.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({key: summary[key] for key in ['original_files', 'reviewed', 'pending', 'read_status_counts']},
                     ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
