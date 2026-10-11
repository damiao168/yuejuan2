"""核对静态目录、图04和SQLite内存结构；不会连接业务数据库。"""
from pathlib import Path
import hashlib
import json
import re
import sqlite3

root = Path(__file__).resolve().parents[3]
evidence = root / 'docs/architecture/evidence'
catalog = json.loads((evidence / 'database-catalog.json').read_text('utf-8'))
spec = json.loads((root / 'docs/architecture/specs/04-database-er.json').read_text('utf-8'))
tables = {table['name']: table for table in catalog['tables']}
errors = []

def check(condition, message):
    if not condition:
        errors.append(message)

for source in catalog['migration_files'] + catalog['additional_schema_sources']:
    check(hashlib.sha256((root / source['file']).read_bytes()).hexdigest() == source['sha256'],
          '源文件已变化: ' + source['file'])

for table in tables.values():
    columns = {column['name'] for column in table['columns']}
    for constraint in table['constraints']:
        check(set(constraint['columns']) <= columns,
              table['name'] + ': 约束引用不存在的字段 ' + constraint['name'])
        if constraint['kind'] != 'foreign_key':
            continue
        parent = tables.get(constraint['referenced_table'])
        check(parent is not None, table['name'] + ': FK父表缺失')
        if parent is not None:
            check(set(constraint['referenced_columns']) <= {c['name'] for c in parent['columns']},
                  table['name'] + ': FK父字段缺失')

# 与生成器独立核对动态FK展开；不能只看CREATE TABLE而漏掉000020的租户约束。
dynamic_fk_count = 0
for source in catalog['migration_files']:
    sql = (root / source['file']).read_text('utf-8-sig')
    for match in re.finditer(r"pg_temp\.ensure_tenant_fk\('([^']+)',\s*'([^']+)',\s*'([^']+)',\s*'([^']+)'\)", sql):
        child, column, parent, name = match.groups()
        dynamic_fk_count += 1
        check(any(c['kind'] == 'foreign_key' and c['name'] == name
                  and c['columns'] == ['tenant_id', column] and c['referenced_table'] == parent
                  and c['referenced_columns'] == ['tenant_id', 'id']
                  for c in tables[child]['constraints']), '动态租户FK漏项: ' + name)

visible_tables = set()
for page in spec['pages']:
    node_tables = {}
    for node in page['nodes']:
        name = node['label'].split('\n')[0]
        check(name in tables, '图04表名不在目录中: ' + name)
        if name in tables:
            node_tables[node['id']] = name
            visible_tables.add(name)
    for edge in page['edges']:
        parent, child = node_tables.get(edge['source']), node_tables.get(edge['target'])
        check(parent is not None and child is not None, '图04边端点缺失')
        if parent and child:
            label_columns = re.search(r'FK \(([^)]+)\)', edge['label'])
            columns = [value.strip() for value in label_columns[1].split(',')] if label_columns else []
            candidates = [c for c in tables[child]['constraints'] if c['kind'] == 'foreign_key'
                          and c['referenced_table'] == parent and c['columns'] == columns]
            check(bool(candidates), '图04连线没有对应字段FK: ' + parent + ' -> ' + child)
            unique_child = any(c['kind'] in ['primary_key', 'unique'] and set(c['columns']) <= set(columns)
                               for c in tables[child]['constraints'])
            check(edge['label'].startswith('1:0..1' if unique_child else '1:N'),
                  '图04基数与PK/UQ不匹配: ' + parent + ' -> ' + child)
check(visible_tables == set(tables), '图04未覆盖全部目录表')

# 只在内存里执行桌面schema，核对SQLite真实语法和目录。不会创建offline.sqlite3。
schema_file = root / 'apps/desktop-client/src-tauri/src/durable_store/schema.rs'
schema = re.search(r'conn\.execute_batch\(\s*"([\s\S]*?)",\s*\)', schema_file.read_text('utf-8'))[1]
connection = sqlite3.connect(':memory:')
connection.executescript(schema)
check(connection.execute('PRAGMA foreign_keys').fetchone()[0] == 1, 'SQLite外键未启用')
sqlite_names = {row[0] for row in connection.execute("SELECT name FROM sqlite_master WHERE type='table'")}
check(sqlite_names == {t['name'] for t in tables.values() if t['database'] == 'SQLite'}, 'SQLite表集合不匹配')
sqlite_fields = sqlite_foreign_keys = sqlite_explicit_indexes = 0
for name in sorted(sqlite_names):
    info = list(connection.execute('PRAGMA table_info(' + name + ')'))
    foreign_keys = list(connection.execute('PRAGMA foreign_key_list(' + name + ')'))
    indexes = list(connection.execute('PRAGMA index_list(' + name + ')'))
    sqlite_fields += len(info)
    sqlite_foreign_keys += len(foreign_keys)
    sqlite_explicit_indexes += sum(row[3] == 'c' for row in indexes)
    check([(row[1], row[2]) for row in info] == [(c['name'], c['type']) for c in tables[name]['columns']],
          'SQLite字段或类型不匹配: ' + name)
    recorded = [c for c in tables[name]['constraints'] if c['kind'] == 'foreign_key']
    check(len(foreign_keys) == len(recorded), 'SQLite外键数量不匹配: ' + name)
    for fk in foreign_keys:
        check(any(c['columns'] == [fk[3]] and c['referenced_table'] == fk[2]
                  and c['referenced_columns'] == [fk[4]] and 'ON DELETE ' + fk[6] in c['definition']
                  for c in recorded), 'SQLite外键动作不匹配: ' + name)
connection.close()

result = {
    'passed': not errors,
    'errors': errors,
    'postgreSQL': {'method': '源码静态交叉核对', 'live_database_checked': False,
                   'migration_count': len(catalog['migration_files']),
                   'dynamic_tenant_foreign_keys_checked': dynamic_fk_count},
    'sqlite': {'method': 'sqlite3内存执行schema及PRAGMA交叉核对', 'persistent_database_modified': False,
               'tables': len(sqlite_names), 'columns': sqlite_fields,
               'foreign_keys': sqlite_foreign_keys, 'explicit_indexes': sqlite_explicit_indexes},
    'diagram': {'pages': len(spec['pages']), 'distinct_tables_covered': len(visible_tables)},
    'limits': ['不证明PostgreSQL迁移可实际执行，不证明部署数据库与源码一致',
               '不替代触发器、存量回填、应用权限和并发行为的人工审读',
               'FK连线核对不证明图纸视觉可读性，需另行渲染']
}
(evidence / 'database-validation.json').write_text(json.dumps(result, ensure_ascii=False, indent=2), 'utf-8')
print(json.dumps(result, ensure_ascii=False, indent=2))
raise SystemExit(0 if result['passed'] else 1)
