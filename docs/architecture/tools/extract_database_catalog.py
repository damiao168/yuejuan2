"""静态清点迁移和本地 schema；不连接或修改业务数据库，不代表人工审读。"""
from pathlib import Path
import re, json, copy, hashlib, subprocess
root=Path(__file__).resolve().parents[3]
out=root/'docs/architecture/evidence'; out.mkdir(parents=True,exist_ok=True)
CONSTRAINT_PREFIX = 'CONSTRAINT '
tables={}; warnings=[]; histories=[]
def clean(sql):
    # 保留换行与偏移，自动抽取只作为清单，不代表逐文件审读。
    pat=r"'(?:''|[^'])*'|\"(?:\"\"|[^\"])*\"|--[^\n]*|/\*.*?\*/"
    return re.sub(pat,lambda m: re.sub(r'[^\n]',' ',m[0]) if m[0].startswith(('--','/*')) else m[0],sql,flags=re.S)
def next_quote(sql, position, quoted):
    # SQL 单引号内部的两个连续单引号表示一个字符，不改变引用状态。
    if quoted and position + 1 < len(sql) and sql[position + 1] == "'":
        return position + 2, quoted
    return position + 1, not quoted


def parens(sql, p):
    level = 0
    quoted = False
    position = p
    while position < len(sql):
        character = sql[position]
        if character == "'":
            position, quoted = next_quote(sql, position, quoted)
            continue
        if not quoted:
            level += (character == '(') - (character == ')')
            if character == ')' and level == 0:
                return position
        position += 1
    raise ValueError('unbalanced SQL')
def splitparts(s):
    parts=[]; a=0;lev=0;quote=False;i=0
    while i<len(s):
        c=s[i]
        if c=="'":
            if quote and i+1<len(s) and s[i+1]=="'":i+=2;continue
            quote=not quote
        elif not quote:
            if c=='(':lev+=1
            elif c==')':lev-=1
            elif c==',' and lev==0:parts.append((s[a:i].strip(),a));a=i+1
        i+=1
    if s[a:].strip():parts.append((s[a:].strip(),a))
    return parts
def loc(path,sql,pos):return {'file':path.relative_to(root).as_posix(),'line':sql.count('\n',0,pos)+1}
def cols(s):return [x.strip().strip('"') for x in s.split(',')]
def constraint(table,s,ev):
    m=re.match(r'CONSTRAINT\s+(\w+)\s+([\s\S]*)',s,re.I)
    name=m[1] if m else None; body=m[2] if m else s
    kind=None; cs=[]; target=None; tc=[]
    if re.match(r'PRIMARY\s+KEY',body,re.I):kind='primary_key';cs=cols(re.search(r'\(([^)]*)\)',body)[1]);name=name or table+'_pkey'
    elif re.match(r'UNIQUE',body,re.I):kind='unique';cs=cols(re.search(r'\(([^)]*)\)',body)[1]);name=name or table+'_'+'_'.join(cs)+'_key'
    elif re.match(r'FOREIGN\s+KEY',body,re.I):
        f=re.search(r'FOREIGN\s+KEY\s*\(([^)]*)\)\s+REFERENCES\s+(\w+)\s*\(([^)]*)\)',body,re.I)
        if not f:return
        kind='foreign_key';cs=cols(f[1]);target=f[2];tc=cols(f[3]);name=name or table+'_'+'_'.join(cs)+'_fkey'
    elif re.match(r'CHECK',body,re.I):kind='check';name=name or table+'_check_'+str(len(tables[table]['constraints']))
    if kind:tables[table]['constraints'][name]={'name':name,'kind':kind,'name_inferred':m is None,'columns':cs,'referenced_table':target,'referenced_columns':tc,'definition':body.strip(),**ev}
def column(table,s,ev):
    m=re.match(r'(\w+)\s+([\s\S]*)',s)
    if not m:return
    n,rest=m[1],m[2]
    typ=re.split(r'\s+(?=NOT\s+NULL|NULL\b|DEFAULT\b|PRIMARY\b|REFERENCES\b|CHECK\b|UNIQUE\b|CONSTRAINT\b|GENERATED\b|COLLATE\b)',rest,1,flags=re.I)[0].strip()
    d={'name':n,'type':typ,'nullable':not bool(re.search(r'NOT\s+NULL'+('' if tables[table].get('database')=='SQLite' else r'|PRIMARY\s+KEY'),rest,re.I)),'not_null_explicit':bool(re.search(r'NOT\s+NULL',rest,re.I)),'definition':rest.strip(),**ev}
    tables[table]['columns'][n]=d
    if re.search(r'PRIMARY\s+KEY',rest,re.I):constraint(table,'PRIMARY KEY ('+n+')',ev)
    if re.search(r'\bUNIQUE\b',rest,re.I):constraint(table,'UNIQUE ('+n+')',ev)
    f=re.search(r'REFERENCES\s+(\w+)\s*\(([^)]*)\)',rest,re.I)
    if f:
        # 行内 REFERENCES 的删除/更新动作是约束的一部分，不能在清单中丢失。
        action=re.match(r'(?:\s+ON\s+(?:DELETE|UPDATE)\s+(?:NO ACTION|RESTRICT|CASCADE|SET NULL|SET DEFAULT))*',rest[f.end():],re.I)[0]
        constraint(table,'FOREIGN KEY ('+n+') REFERENCES '+f[1]+'('+f[2]+')'+action,ev)
    ck=re.search(r'CHECK\s*\(',rest,re.I)
    if ck:constraint(table,CONSTRAINT_PREFIX+table+'_'+n+'_check '+rest[ck.start():],ev)
def semicolon(sql,p):
    i=p;quote=False
    while i<len(sql):
        if sql[i]=="'":
            if quote and i+1<len(sql) and sql[i+1]=="'":i+=2;continue
            quote=not quote
        elif sql[i]==';' and not quote:return i
        i+=1
    return len(sql)

def rls_loops(sql):
    # 单次遍历识别数组/循环边界；缺少 END LOOP 时不反复回溯整份迁移。
    tokens = re.compile(r'(?P<header>FOREACH\s+\w+\s+IN\s+ARRAY\s+ARRAY\s*\[)|(?P<array_end>\]\s*LOOP)|(?P<body_end>END\s+LOOP)', re.I)
    header_start = array_start = body_start = None
    array = ''
    for token in tokens.finditer(sql):
        if header_start is None:
            if token.lastgroup == 'header':
                header_start, array_start = token.start(), token.end()
            continue
        if body_start is None:
            if token.lastgroup == 'array_end':
                array, body_start = sql[array_start:token.start()], token.end()
            continue
        if token.lastgroup == 'body_end':
            yield header_start, array, sql[body_start:token.start()]
            header_start = array_start = body_start = None

files=sorted((root/'services/api-gateway/migrations').glob('*.sql')) + [root/'infra/docker-compose/docker-compose.yml',root/'apps/desktop-client/src-tauri/src/durable_store/schema.rs']
for path in files:
    raw=path.read_text('utf-8-sig');sql=clean(raw);events=[]
    matchsql=re.sub(r"'(?:''|[^'])*'",lambda m:re.sub(r'[^\n]',' ',m[0]),sql)
    # 字面量内部的ALTER format没有真实表名，因此不会进入此模式。
    for m in re.finditer(r'\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s*\(',matchsql,re.I):
        p=m.end()-1;end=parens(sql,p);events.append((m.start(),'create',m[1],sql[p+1:end],p+1))
    for m in re.finditer(r'\bALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(\w+)\s+',matchsql,re.I):events.append((m.start(),'alter',m[1],sql[m.end():semicolon(sql,m.end())],m.end()))
    for m in re.finditer(r'\bCREATE\s+(UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+(\w+)\s*',matchsql,re.I):events.append((m.start(),'index',m[3],sql[m.start():semicolon(sql,m.end())],m.end()))
    for m in re.finditer(r'\bDROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?(\w+)',matchsql,re.I):events.append((m.start(),'dropindex',m[1],'',m.end()))
    for m in re.finditer(r"(?:PERFORM\s+)?pg_temp\.ensure_tenant_identity\('([^']+)',\s*'([^']+)'\)",sql,re.I):events.append((m.start(),'dynunique',m[1],m[2],m.start()))
    for m in re.finditer(r"(?:PERFORM\s+)?pg_temp\.ensure_tenant_fk\('([^']+)',\s*'([^']+)',\s*'([^']+)',\s*'([^']+)'\)",sql,re.I):events.append((m.start(),'dynfk',m[1],(m[2],m[3],m[4]),m.start()))
    for pos,kind,name,body,off in sorted(events):
        ev=loc(path,raw,pos)
        if kind=='dropindex':
            for t in tables.values():t['indexes'].pop(name,None)
            continue
        if kind=='create':
            t={'name':name,'database':'SQLite' if path.suffix=='.rs' else 'PostgreSQL','columns':{},'constraints':{},'indexes':{},'history':[], 'rls':False,'create_evidence':ev}
            tables[name]=t
            for part,p in splitparts(body):
                pe=loc(path,raw,off+p+len(body[p:])-len(body[p:].lstrip()))
                if part.upper().startswith('LIKE '):
                    src=part.split()[1];t['columns']=copy.deepcopy(tables[src]['columns']);t['constraints']={k:v for k,v in copy.deepcopy(tables[src]['constraints']).items() if v['kind']!='foreign_key'};t['like_source']=src
                    for c in t['columns'].values():c['inherited_from']=src
                elif re.match(r'(?:CONSTRAINT\b|PRIMARY\s+KEY\b|UNIQUE\b|FOREIGN\s+KEY\b|CHECK\b)',part,re.I):constraint(name,part,pe)
                else:column(name,part,pe)
        elif name not in tables:warnings.append({'kind':'unknown_table','table':name,**ev});continue
        elif kind=='alter':
            for action,p in splitparts(body):
                pe=loc(path,raw,off+p+len(body[p:])-len(body[p:].lstrip()))
                a=re.sub(r'^ADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?','',action,flags=re.I)
                if a!=action:column(name,a,pe);continue
                a=re.sub(r'^ADD\s+','',action,flags=re.I)
                if a!=action:constraint(name,a,pe);continue
                m=re.match(r'DROP\s+CONSTRAINT\s+(?:IF\s+EXISTS\s+)?(\w+)',action,re.I)
                if m:tables[name]['constraints'].pop(m[1],None);continue
                m=re.match(r'ALTER\s+COLUMN\s+(\w+)\s+TYPE\s+(.*)',action,re.I|re.S)
                if m and m[1] in tables[name]['columns']:tables[name]['columns'][m[1]]['type']=m[2].strip();tables[name]['columns'][m[1]]['type_evidence']=pe;continue
                m=re.match(r'ALTER\s+COLUMN\s+(\w+)\s+(SET|DROP)\s+NOT\s+NULL',action,re.I)
                if m and m[1] in tables[name]['columns']:tables[name]['columns'][m[1]]['nullable']=m[2].upper()=='DROP';continue
                m=re.match(r'ALTER\s+COLUMN\s+(\w+)\s+(SET|DROP)\s+DEFAULT\s*(.*)',action,re.I|re.S)
                if m and m[1] in tables[name]['columns']:tables[name]['columns'][m[1]]['default_alter']=m[3].strip();continue
                if not re.match(r'(?:VALIDATE\s+CONSTRAINT|ENABLE\s+ROW|FORCE\s+ROW|DISABLE\s+TRIGGER|ENABLE\s+TRIGGER)',action,re.I):warnings.append({'kind':'unhandled_alter','table':name,'action':action,**pe})
        elif kind=='index':
            m=re.match(r'CREATE\s+(UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+\w+',body,re.I)
            tables[name]['indexes'][m[2]]={'name':m[2],'unique':bool(m[1]),'definition':body,'partial':bool(re.search(r'\bWHERE\b',body,re.I)),**ev}
        elif kind=='dynunique':constraint(name,CONSTRAINT_PREFIX+body+' UNIQUE (tenant_id, id)',ev)
        elif kind=='dynfk':constraint(name,CONSTRAINT_PREFIX+body[2]+' FOREIGN KEY (tenant_id, '+body[0]+') REFERENCES '+body[1]+' (tenant_id, id)',ev)
        tables[name]['history'].append({'operation':kind,'definition':body,**ev})
    # RLS数组及显式表策略只记静态源码证据，未核验已部署数据库。
    if 'ENABLE ROW LEVEL SECURITY' in sql:
        for loop_start, array, body in rls_loops(sql):
            if 'ENABLE ROW LEVEL SECURITY' in body:
                for n in re.findall(r"'([^']+)'", array):
                    if n in tables:tables[n]['rls']=True;tables[n]['rls_evidence']=loc(path,raw,loop_start)
        for m in re.finditer(r'ALTER\s+TABLE\s+(\w+)\s+ENABLE\s+ROW\s+LEVEL\s+SECURITY',sql,re.I):
            if m[1] in tables:tables[m[1]]['rls']=True;tables[m[1]]['rls_evidence']=loc(path,raw,m.start())
    if path.name.startswith('000072_'):
        for n in ['submission','arbitration_task','appeal','submission_grade','model_approval','agent_worker_task']:
            constraint(n,CONSTRAINT_PREFIX+n+'_revision_positive CHECK (revision > 0)',loc(path,raw,raw.index('FOREACH')))
    histories.append({'file':path.relative_to(root).as_posix(),'sha256':hashlib.sha256(path.read_bytes()).hexdigest(),'bytes':path.stat().st_size,'ddl_events':len(events)})
for t in tables.values():
    t['columns']=list(t['columns'].values());t['constraints']=list(t['constraints'].values());t['indexes']=list(t['indexes'].values())
result={'source':'当前工作区PostgreSQL及桌面SQLite schema静态重建，非已部署数据库反向工程','head':'d002c7b1f928e938b841e1203976c763b03e8485','database_migrations_executed':False,'method':'按迁移编号抽取CREATE/ALTER/索引，展开ensure_tenant_identity/fk和RLS数组；约束保留源码证据；自动提取不等同人工审读','limitations':['尚未运行迁移；条件执行受部署存量数据影响','自动推导的PostgreSQL约束名并非数据库实查，未命名CHECK可能保留历史定义，请按history和实际DDL核验','SQLite非INTEGER PRIMARY KEY未必隐含NOT NULL，目录按显式DDL记录空值规则','PL/pgSQL触发器、数据回填与业务跨表语义需单独审读','LIKE INCLUDING ALL不复制FK；继承索引名称仅保留来源','同名约束可能有PostgreSQL自动命名差异；未解析项列于warnings'], 'migration_files':[h for h in histories if '/migrations/' in h['file']],'additional_schema_sources':[h for h in histories if '/migrations/' not in h['file']],'tables':list(tables.values()),'warnings':warnings}
result['head']=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()
(out/'database-catalog.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),'utf-8')
print(json.dumps({'tables':len(tables),'columns':sum(len(t['columns']) for t in tables.values()),'foreign_keys':sum(c['kind']=='foreign_key' for t in tables.values() for c in t['constraints']),'indexes':sum(len(t['indexes']) for t in tables.values()),'rls_tables':sum(t['rls'] for t in tables.values()),'warnings':warnings},ensure_ascii=False,indent=2))
