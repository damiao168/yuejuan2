"""精简图内文字，突出逻辑；详细解释与出处仍保存在规格、原生属性和审计报告。"""
from pathlib import Path
import json
import re

BASE = Path(__file__).resolve().parents[1]

BOUNDARIES = {
    'f01':'外部身份服务：未现场验收',
    'f02':'学校 AI 助手：仅学校管理员',
    'f03':'题库素材 ≠ 考试题目\n实例化需考试管理 + 题库读取权限',
    'f04':'考试工作区：按分区授权',
    'f05':'待确认：YOLO / 未知版面建模板',
    'f06':'识别证据 ≠ 人工确认 / 最终成绩',
    'f07':'人工双评 ≠ AI Panel\n主观题建议需人工确认',
    'f08':'旧发布与新快照并存\n门禁与学生读取来源不同',
    'f09':'旧模型入口 → 共用模型配置页',
    'f10':'独立教师门户：仅规划\nWindows 能力与浏览器回退分开',
}

MODEL_RUNTIME_LABEL = '本地 / 外部模型'

# 只改显示文案，不增删业务关系，也不把接入缺口改成实现保证。
TEXT = {
 '02': {
  'logical': {'web':'管理端 / 教师\nReact', 'student':'学生门户\nReact', 'desktop':'桌面工作站\nTauri / Rust', 'sdk':'共享 SDK', 'api':'Go API\n认证 · 授权 · 幂等', 'identity':'身份与组织', 'prepare':'考试与试卷', 'capture':'采集与处理', 'grading':'评分与发布', 'stores':'领域 Store', 'compute':'图像 / AI 计算', 'db':'PostgreSQL\n业务事实', 'object':'MinIO\n原图 / 裁片 / 附件', 'redis':'Redis\n认证限流'},
  'runtime': {'go':'Go 业务服务', 'runtime':'Worker Runtime\n任务 / 租约', 'workers':'Python Workers', 'image':'图像处理\n质量 / 配准 / ROI', 'goai':'评分 / 试卷解析', 'agent':'grading-agent', 'models':MODEL_RUNTIME_LABEL, 'govern':'模型治理', 'panel':'三智能体内核\n普通生产入口未接入', 'maintenance':'后台维护', 'partialinfra':'Qdrant：健康检查\noutbox：日志投递'}},
 '03': {'overview': {'server':'server\n组合根', 'auth':'认证 / HTTP / 日志', 'db':'SQL / pgx', 'web':'Web / 学生 / 桌面', 'sdk':'SDK', 'workers':'Python Worker', 'debt':'维护债务\n宽 Store / 大页面', 'constraints':'静态 import 无环\n运行时耦合另行核对'}},
 '05': {
  'intake': {'create':'创建考试','configure':'配置试卷 / 名单 / 模板','ready':'准备确认 → 开始采集','capture':'上传答卷','decode':'解码为页面','quality':'质量检测','quality_result':'质量通过？','review_quality':'人工质量 / 身份复核','template':'确定已有模板','registration':'配准 → ROI 切题','segments':'答案裁片','recognize':'另行启动 OCR / 数学理解','next':'进入评分'},
  'grading_release': {'start':'启动评分','mode':'冻结的 scoring_mode','human':'人工评分\n单评 / 双评 / 手工','rule':'RULE_AUTO\n文本规则','omr':'RULE_AUTO\nOMR 规则','ai_assist':'AI_ASSIST / AI_FAST_CONFIRM\n人工确认','confirm_rule':'允许自动确认？','review':'人工终评','ai_optional':'独立 AI 建议入口','finalize':'汇总成绩','confirm':'确认成绩','release':'创建并发布快照','student':'学生读取当前快照'},
  'publication_paths': {'old_route':'旧发布入口','old_gate':'旧质量检查','old_publish':'锁定工作成绩','old_student':'旧学生成绩接口','new_route':'快照发布入口','new_gate':'完整性 / 质量 / 重评门禁','new_publish':'发布快照 → 更新当前指针','new_student':'新学生结果接口','gap':'问题：旧入口绕过新增门禁','split':'问题：两套公开来源可能分裂'}},
 '06': {
  'template_routing': {'upload':'上传原件','decode':'解码 / 条码观测','quality':'图像质量','q':'质量通过？','blocked':'人工复核','controlled':'已有可信模板？','bound':'锁定模板','candidates':'已有模板候选匹配','score':'结构 + 几何验证','match':'匹配成功 → 绑定','unknown':'未知 / 歧义 / 冲突','next':'配准任务','missing':'待确认：YOLO / 未知版面建模板'},
  'registration_crops': {'input':'标准化页面 + 模板','guard':'需要模板守卫？','fallback':'小范围重匹配\n不替换考试锁定模板','cv':'像素一致 / ORB → AKAZE','safe':'几何通过？','terminal':'算法失败 → 人工','crop':'透视校正 → ROI 裁片','persist':'保存配准结果','runtime':'完成 Runtime\n与领域提交分事务','confirm':'人工确认','manual':'四点校正','apply':'应用 / 撤销'},
  'blank_boundaries': {'page_input':'整页图像','page_detect':'像素空白概率','page_review':'质量复核','omr_input':'选项 ROI','omr_detect':'选项强度判断','omr_review':'未选中 → 人工复核','ocr_input':'文本 OCR','ocr_detect':'无文本块','ocr_review':'人工判断','gap':'待确认：主观题统一空白 / 自动零分'}},
 '07': {
  'actual_entry': {'entry':'单题 / AI 批次','worker':'主观题 Worker','prepare':'租约 / 来源 / 版本校验','gate':'允许 AI？','abstain':'拒绝 / 转人工','input':'冻结答案 / 细则 / 证据','adapter':'单一 activeAdapter','agent':'grading-agent','settle':'服务端校验 / 算分','complete':'保存建议 / 完成任务','human':'教师确认','missing':'A/B/C 内核\n普通生产入口未接入','version':'run / grade 版本记录'},
  'panel_kernel': {'policy':'批准的 Panel 策略','panel':'创建 / 恢复 Panel','a':'A 独立盲评','b':'B 独立盲评','derive':'服务端算分','compare':'A/B 精确共识？','consensus':'采纳 A 共识','c':'C 盲仲裁','safe':'C 结果满足策略？','human':'人工复核任务','resolved':'采纳 C 仲裁','boundary':'内核可调用\n未默认接入生产路由'},
  'failure_cost': {'request':'冻结版本 / 请求键','cache':'成功缓存','session':'供应商会话','retry':'响应修复 / 有限重试','go_retry':'Go HTTP 重试','worker_retry':'Worker 任务重试','usage':'调用 / Token 计量','gap':'问题：grade / task / run 分步提交','panel_gap':'问题：人工终态早于任务关联'},
  'multimodal_math': {'v1':'v1 文本评分','v2_entry':'数学 v2 条件入口','crop':'读取有效裁片','build':'图片 / 文本证据','app':'grade-v2','messages':'多模态消息','local':'本地 chat/completions','native':'DashScope 原生协议\n图片支持待验证','candidate':'候选评分点 / 证据','settle':'按冻结细则结算','human':'歧义 / 质量低 → 人工','empty':'v2 仍要求答案文本'}},
 '08': {
  'api-overview': {'web':'Web 管理 / 教师','student':'学生门户','desktop':'桌面客户端','transport':'SDK / API 包装','mux':'路由 / Guard','business':'Handler → Store','auth':'鉴权顺序有入口差异','persistence':'PG / 文件对象','worker':'Worker / AI','catalog':'完整接口见目录','health':'/health 由 HandleFunc 注册'},
  'files-workers': {'browser':'上传','upload':'文件接口','metadata':'PG 文件元数据','objects':'MinIO 二进制','intake':'领域登记 / 排任务','runtime':'Runtime 任务','scope':'任务范围 / 租约校验','worker':'Worker 计算','download':'受控下载','preview':'图片 / Blob 预览','local':'本地 SQLite 队列\n加密载荷 / Spool','boundary':'对象写入与 PG 分离\n失败由对账恢复'},
  'auth-order': {'request':'请求','session':'认证','generic':'通用 Guard','cache':'收据命中？','resource':'资源范围','permission':'业务权限','strict':'关键 Guard\n授权后重放','qbank':'题库专用 Guard','mfa':'MFA / 助手专用 Guard','worker':'Worker Capability','risk':'问题：通用重放先于当前授权'}},
 '09': {
  'runtime': {'queued':'queued','leased':'leased','running':'running','success':'succeeded','retry':'可重试且未达上限？','failed':'failed','dead':'dead_letter','expired':'租约过期 → attempt 记录','cancel':'cancelled','manual':'显式 Requeue','boundary':'入口校验租约\n提交原子性见下一页'},
  'business_states': {'start':'创建 scoring_run','processing':'processing','review':'needs_review','failed':'failed','done':'completed','cancelling':'cancelling','cancelled':'cancelled','first':'人工双评','diff':'分差在阈值内？','auto':'auto_finalized','arb':'needs_arbitration','notice':'人工双评与 AI Panel 分开'},
  'commit_boundaries': {'quality':'质量：同事务','match':'模板匹配：同事务','ocr':'OCR：同事务','reg_write':'配准：先提交领域结果','reg_finish':'再完成 Runtime','reg_effect':'问题：两处状态可能分裂','ai_grade':'主观题：先写 grade / 完成 task','ai_run':'再更新 run','ai_effect':'问题：末步失败难恢复','panel_state':'Panel：先写人工终态','panel_replay':'再创建 / 关联任务','panel_effect':'问题：终态重放不补任务'}},
 '10': {
  'authorization': {'human':'人类角色\n平台 / 学校 / 教师 / 学生等','service':'服务身份','auth':'认证 / 当前会话','idem':'通用收据重放','resource':'资源范围 / 业务权限','recent':'关键操作\n授权 → 近期认证 → 重放','task':'Worker 能力 / 租约','rls':'SQL 范围 + 部分表 RLS','boundary':'tenant 与 school 分开'},
  'credential': {'admin':'保存模型配置','cipher':'AES-GCM\ntenant / config AAD','store':'PG 密文','resolve':'受控解密','go':'业务处理器 / Agent','external':MODEL_RUNTIME_LABEL,'session':'会话 Token Hash\n密码 Argon2id','protect':'CSRF / 限流 / 审计'},
  'deployment': {'client':'浏览器 / 桌面宿主','nginx':'Nginx 入口','web':'Web 静态容器','api':'Go API 容器','data':'PG / Redis / MinIO / Qdrant','workers':'Python Worker\nOCR → 数学验证：HTTPS','agent':'grading-agent\n内部 HTTPS / 证书','model':MODEL_RUNTIME_LABEL,'ops':'迁移 / 备份 / 恢复','observe':'Prometheus / Grafana\n可选 profile'}},
}

# 这些短标题已用于大字号排版；再次简化时保留首行，正文和完整说明沿用原规则。
TITLE_OVERRIDES = {
    ('01', 'f01', 'F01.01.features'): '密码 / Token / 微信登录',
    ('01', 'f01', 'F01.02.features'): '平台启用与学校管理入口',
    ('01', 'f01', 'F01.03.features'): '管理员、教师、审计员、学生',
    ('01', 'f02', 'F02.01.features'): '学校初始化与机构配置',
    ('01', 'f02', 'F02.02.features'): '学生名单、导入与班级归属',
    ('01', 'f02', 'F02.03.features'): '学校管理员聊天 / SSE',
    ('01', 'f03', 'F03.01.features'): '题库内容、资产与元数据',
    ('01', 'f03', 'F03.02.features'): '试卷上传、解析与人工确认',
    ('01', 'f03', 'F03.03.features'): '题目、答案、量规与规则版本',
    ('01', 'f04', 'F04.01.features'): '新建单科考试 / 多科场次',
    ('01', 'f04', 'F04.02.features'): '学生范围、名单与候选人刷新',
    ('01', 'f04', 'F04.03.features'): '13 个管理分区承载业务阶段',
    ('01', 'f05', 'F05.01.features'): '模板 ROI 编辑、锁定与克隆',
    ('01', 'f05', 'F05.02.features'): '采集批次、解码与多页 PNG',
    ('01', 'f05', 'F05.03.features'): '页/学生匹配确认、拆分合并',
    ('01', 'f05', 'F05-boundary'): '待确认：YOLO 未知版面模板',
    ('01', 'f06', 'F06.01.features'): '答卷 OCR 创建、读取与提交',
    ('01', 'f06', 'F06.02.features'): '答案分段生成、修订与读图',
    ('01', 'f06', 'F06.03.features'): '数学任务、产物与证据核验',
    ('01', 'f06', 'F06-boundary'): '识别≠人工确认 / 最终成绩',
    ('01', 'f07', 'F07.01.features'): '客观题规则/OMR 评分运行',
    ('01', 'f07', 'F07.02.features'): '领取、续租、分派、草稿、提交',
    ('01', 'f07', 'F07.03.features'): 'A/B 并行盲评与差异比较',
    ('01', 'f08', 'F08.01.features'): '结分、质检、缺考与名单核对',
    ('01', 'f08', 'F08.02.features'): '考试/班级/题目/阅卷质量报告',
    ('01', 'f08', 'F08.03.features'): '本人已发布成绩与逐题反馈',
    ('01', 'f09', 'F09.01.features'): 'API 配置、探测与学校授权',
    ('01', 'f09', 'F09.02.features'): '样卷、校准、答案组与 Seed',
    ('01', 'f09', 'F09.03.features'): '旧模型写接口返回 410',
    ('01', 'f10', 'F10.01.features'): '文件导入、批次与持久上传队列',
    ('01', 'f10', 'F10.02.features'): '下载、加密草稿与本机授权',
    ('01', 'f10', 'F10.03.features'): '健康、依赖、指标与文件对账',
    ('06', 'template_routing', 'missing'): '待确认：YOLO 未知版面模板',
    ('06', 'blank_boundaries', 'gap'): '待确认：空白主观题自动零分',
    ('07', 'failure_cost', 'gap'): '问题：评分链路分步提交',
    ('08', 'api-overview', 'health'): '/health 直接注册',
    ('08', 'identity-org', 'row1-handler'): '平台学校处理器',
    ('08', 'identity-org', 'row1-data'): '平台学校数据',
    ('08', 'identity-org', 'row2-page'): '学校初始化 / 班级 / 学生',
    ('08', 'exam-paper', 'row0-page'): '考试列表 / 新建 / 总览',
    ('08', 'exam-paper', 'row1-handler'): '题库实例化处理',
    ('08', 'exam-paper', 'row1-data'): '题库 / 试卷数据',
    ('08', 'exam-paper', 'row2-data'): '试卷 / 文件数据',
    ('08', 'capture-ocr', 'row0-data'): '采集数据 / 任务运行时',
    ('08', 'capture-ocr', 'row1-handler'): '图像质量 / 采集处理',
    ('08', 'capture-ocr', 'row1-data'): '质量 / 配准运行 / 分段',
    ('08', 'capture-ocr', 'row2-handler'): 'OCR / 分段处理',
    ('08', 'capture-ocr', 'row3-handler'): '分段 / 阅卷处理',
    ('08', 'capture-ocr', 'row3-data'): '当前分段 / 证据 / 私有对象',
    ('08', 'grading-release', 'row0-handler'): '评分 / 主观题处理',
    ('08', 'grading-release', 'row0-data'): '评分运行 / AI 评分 / 阅卷任务',
    ('08', 'grading-release', 'row2-handler'): '成绩 / 发布 / 报告',
    ('08', 'grading-release', 'row3-handler'): '学生入口 / 已发布题申诉',
    ('08', 'grading-release', 'row3-data'): '成绩快照 / 公开批注',
    ('08', 'governance-chat', 'row0-handler'): '模型治理处理',
    ('08', 'governance-chat', 'row1-handler'): '模型治理 / 学校助手',
    ('08', 'governance-chat', 'row2-handler'): '模型治理 / 评估 / 校准',
    ('08', 'governance-chat', 'row3-data'): '冻结答案/量规/模型/提示',
    ('09', 'commit_boundaries', 'ai_grade'): '主观题：先存分，再完成任务',
    ('10', 'authorization', 'boundary'): '旧键：同操作者有效新会话',
    ('08', 'exam-paper', 'row1-page'): '题库 → 试卷工作区',
    ('08', 'capture-ocr', 'row3-page'): '阅卷 / 数学证据',
    ('08', 'grading-release', 'row1-page'): '匿名阅卷 / 仲裁',
    ('08', 'grading-release', 'row2-data'): '旧状态发布',
    ('08', 'governance-chat', 'row3-page'): '评分命令 → 执行',
}

def display_index(table):
    indexes = table.get('indexes', []) if table else []
    if not indexes:
        return None
    return next((index for index in indexes if index['unique']), indexes[0])


def append_selected_fields(fields, selected, predicate, limit):
    for field in fields:
        if field not in selected and predicate(field) and len(selected) < limit:
            selected.append(field)


def index_display_lines(index):
    if not index:
        return []
    definition = ' '.join(index['definition'].split())
    columns = re.search(r'\bON\s+\w+\s*\((.*?)\)', definition, re.I)
    if columns:
        prefix = 'UQ IDX' if index['unique'] else 'IDX'
        lines = [prefix + ' (' + columns.group(1) + ')']
    else:
        lines = ['IDX ' + index['name']]
    where = re.search(r'\bWHERE\s+(.+)', definition, re.I)
    if where:
        lines.append('WHERE ' + where.group(1))
    return lines


def entity_text(lines, table):
    fields = [field for field in lines[1:] if ':' in field and not field.startswith(('索引', 'UQ索引', 'RLS'))]
    selected = [field for field in fields if '[PK' in field or field.startswith('tenant_id:')]
    append_selected_fields(fields, selected, lambda field: '[FK' in field, 5)
    append_selected_fields(fields, selected, lambda field: re.match(r'(status|state|version|revision|generation|is_current|score|total_score|name|answer_version|rubric_version)', field), 7)
    unique = next((line for line in lines if line.startswith('UQ ')), None)
    if unique and len(unique) < 74:
        selected.append(unique)
    # 索引属于 ER 验收内容；每表仍显示一个真实索引和原有部分索引条件。
    selected.extend(index_display_lines(display_index(table)))
    return '\n'.join([lines[0], *selected])

def feature_node_display(node, page, lines):
    if node['id'].endswith('.features'):
        return '\n'.join(lines[1:])
    if page['id'] == 'feature-overview':
        return lines[0]
    if node['id'].endswith('-boundary'):
        return BOUNDARIES[page['id']]
    return '\n'.join(lines[:2])


def entity_node_display(node, lines, tables):
    table = tables.get(lines[0])
    index = display_index(table)
    if index:
        evidence = f"{index['file']}:{index['line']}"
        if evidence not in node['evidence'].split(';'):
            node['evidence'] += ';' + evidence
    return entity_text(lines, table)


def node_display(node, page, number, lines, tables):
    if node.get('kind') == 'entity':
        return entity_node_display(node, lines, tables)
    if number == '01':
        return feature_node_display(node, page, lines)
    if number == '08' and page['id'] not in TEXT['08']:
        # API 域页保留代表端点；完整端点和 guard 说明继续保存在对象属性中。
        count = 3 if node['id'].endswith('-api') else 2
        return '\n'.join(lines[:count])
    return TEXT.get(number, {}).get(page['id'], {}).get(node['id'], lines[0])


def simplify_node(node, page, number, tables):
    # SQLite 普通连接只加密草稿载荷和 Spool，显示文案不能暗示整库加密。
    node['label'] = node['label'].replace('加密 SQLite + Spool 保存队列 / 草稿', 'SQLite 队列与加密载荷 / Spool').replace('加密 SQLite 草稿', 'SQLite 加密草稿载荷')
    display = node_display(node, page, number, node['label'].split('\n'), tables)
    first_line = TITLE_OVERRIDES.get((number, page['id'], node['id']))
    if first_line is not None:
        _, separator, remaining = display.partition('\n')
        display = first_line + (separator + remaining if separator else '')
    node.update(display_label=display, show_evidence=False)
    return len(node['label']), len(display)


def simplify_edge(edge, page, number):
    value = edge.get('label', '')
    cardinality = re.match(r'^(1:(?:0\.\.1|N|1))\b', value) if number == '04' else None
    if cardinality:
        display = cardinality.group(1)
    elif len(value) <= 28:
        display = value
    else:
        display = value.split('；')[0].split('，')[0]
    if number == '10' and page['id'] == 'deployment' and edge['source'] == 'workers' and edge['target'] == 'api':
        display = '任务 HTTP'
    edge['display_label'] = display


def main():
    nodes = before = after = 0
    catalog = json.loads((BASE / 'evidence/database-catalog.json').read_text(encoding='utf-8'))
    tables = {table['name']: table for table in catalog['tables']}
    TEXT['06']['template_routing']['controlled'] = '受控输入已有模板？'
    TEXT['07']['failure_cost'].update(session='并发槽 / 模型超时\n输出 Token 上限', retry='无效响应：修复 / 有限重试', go_retry='Go HTTP 有限重试\n模型超时不重试', usage='Token / 延迟计量\n全链费用无硬上限')
    TEXT['10']['authorization'].update(human='平台 / 租户 / 学校管理员\n教师 / 阅卷员 / 仲裁员 / 审计员 / 学生', service='页面处理 / 主观评分 Worker\n任务绑定 + 租约校验', boundary='旧键重放：同 actor 的新有效会话\n收据命中可能跳过当前业务授权')
    for path in sorted((BASE / 'specs').glob('*.json')):
        spec = json.loads(path.read_text(encoding='utf-8-sig'))
        number = spec['file'][:2]
        spec['compact'] = True
        for page in spec['pages']:
            page['display_note'] = ''
            for node in page['nodes']:
                original_count, display_count = simplify_node(node, page, number, tables)
                nodes += 1
                before += original_count
                after += display_count
            for edge in page.get('edges', []):
                simplify_edge(edge, page, number)
        path.write_text(json.dumps(spec, ensure_ascii=False, indent=2), encoding='utf-8')
    report = {'nodes': nodes, 'original_label_characters': before, 'visible_label_characters': after,
              'reduction_percent': round((1 - after / before) * 100, 1), 'details_preserved': True, 'relations_changed': False}
    (BASE / 'evidence/diagram-simplification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(report))

if __name__=='__main__':
    main()
