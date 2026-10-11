"""从数据库目录生成图04；人工审读结论只能来自有匹配哈希的旁置记录。"""
from pathlib import Path
import json,hashlib
root=Path(__file__).resolve().parents[3];ep=root/'docs/architecture/evidence';c=json.loads((ep/'database-catalog.json').read_text('utf-8'));ts={t['name']:t for t in c['tables']}
manifest=json.loads((ep/'database-reviewed-files.json').read_text('utf-8'))['files']
complete_paths=set()
for position,entry in enumerate(manifest):
 path=root/entry['file']
 # 文件变化后旧审读结论失效。自动生成器不会把新的文件或DDL判定为已审读。
 if path.exists() and entry.get('source_sha256')==hashlib.sha256(path.read_bytes()).hexdigest():
  if entry['read_status']=='complete':complete_paths.add(entry['file'])
 else:
  manifest[position]=dict(entry,read_status='not_manually_reviewed',analysis_status='pending')
groups=[
 ('auth','认证与RBAC','tenant app_user role permission user_role role_permission auth_session audit_log'),
 ('identity','身份激活、风险与MFA','auth_activation auth_recovery auth_trusted_device auth_risk_event auth_totp auth_mfa_recovery_code auth_mfa_challenge auth_security_notification_intent wechat_identity auth_wechat_login_challenge'),
 ('org','组织、学籍与学校','school grade school_class student teacher_class academic_year grade_cohort student_enrollment'),
 ('exam','整场考试、学科与冻结名册','exam_session exam exam_class exam_candidate_snapshot exam_blueprint_section exam_template exam_template_subject exam_template_section exam_student_attendance exam_readiness_snapshot'),
 ('paper','试卷、题目、答案与评分细则','exam_paper question question_answer_key rubric_version question_rubric question_solution'),
 ('import','试卷资料导入与识别代次','paper_import_job paper_import_source paper_import_parse_input paper_import_formula_input paper_import_run'),
 ('capture','答卷采集、批次与页面','capture_batch capture_file capture_page capture_operation submission submission_page capture_upload_session capture_upload_chunk scanner_profile'),
 ('registration','质量、配准与模板复用','submission_page_quality_run page_registration_run page_registration_correction page_template_match_run answer_sheet_template exam_answer_sheet_template_binding omr_calibration_session omr_calibration_case'),
 ('print','答题卡打印与学生纸张身份','answer_sheet_print_batch answer_sheet_print_sheet answer_sheet_print_page'),
 ('runtime','任务来源、执行与投影','orchestration_run agent_task agent_job agent_worker_task agent_worker_task_attempt agent_worker_heartbeat ocr_task ocr_result submission_page_processing_state operational_exception processing_projection_cursor'),
 ('objective','答案与客观评分版本','answer_segment answer_segment_answer scoring_rule scoring_run answer_candidate omr_run question_grade'),
 ('review','人工复核、双评与成绩事实','review_task human_grade review_draft double_mark_policy double_mark_session arbitration_task final_grade submission_grade review_annotation review_comment_template'),
 ('subjective','主观AI评分、面板与评估门禁','ai_grade subjective_grading_run subjective_grading_batch subjective_grading_panel subjective_panel_policy grading_panel_evaluation_observation subjective_batch_enqueue_plan'),
 ('model','模型治理、配置与审批','model_provider model_deployment managed_model_api_config model_role_binding tenant_model_policy tenant_model_policy_model model_call_fact model_sandbox_approval model_evaluation_run model_evaluation_candidate model_approval'),
 ('platform','学校运维、调用计量与投影','model_usage_event model_usage_daily platform_school_metrics platform_school_model_role activity_event'),
 ('assessment','学科画像、题型、冻结快照与证据','subject_profile question_archetype question_assessment_config exam_question_snapshot scoring_evidence'),
 ('calibration','金标卷与阅卷员校准','grading_gold_paper grading_gold_paper_version grader_calibration_policy grader_calibration_session grader_calibration_attempt grader_question_qualification'),
 ('group','答案组、抽样与决策','answer_group answer_group_member answer_group_sample_review answer_group_decision answer_group_automation_candidate'),
 ('quality','种子题、漂移与回评','seed_sampling_policy seed_sampling_cursor seed_task seed_observation grader_quality_window grading_quality_incident backmark_batch backmark_item backmark_grade'),
 ('evaluation','AI资格、评估与校准','ai_eligibility_policy ai_eligibility_decision grading_evaluation_run grading_evaluation_observation grading_evaluation_slice_metric grading_evaluation_response_difficulty model_calibration model_calibration_evidence model_score_candidate ai_human_disagreement'),
 ('release','不可变成绩发布与质量门禁','score_release score_release_current score_release_item score_release_question score_release_anonymous_page release_gate_policy release_gate_evidence release_gate_waiver_request release_gate_waiver_decision report'),
 ('appeal','重评、题目申诉与调整','regrade_job regrade_item regrade_event question_appeal question_appeal_event appeal score_adjustment'),
 ('math','数学理解证据、纠错与试点门禁','math_understanding_artifact math_understanding_correction math_pilot_gate_evaluation'),
 ('bank','题库内容版本、ACL与发布','question_bank question_bank_acl question_bank_item question_bank_item_version question_bank_review question_bank_answer_version question_bank_rubric_version question_bank_item_asset'),
 ('bankmeta','题库模板、元数据、分组与导入','question_bank_rubric_template question_bank_rubric_template_version question_bank_metadata_schema question_bank_item_metadata question_bank_group question_bank_group_member question_bank_group_acl question_bank_import'),
 ('reliability','文件生命周期、审计命令与迁移','file_asset file_reconciliation_run file_reconciliation_finding event_outbox idempotency_record business_command_receipt exam_management_command exam_archive_guarded_table schema_migration'),
 ('sqlite','桌面离线SQLite与本地关系','local_asset scan_batch_local upload_session upload_chunk local_asset_ingest local_asset_chunk sync_event draft')]
seen=set()
for gid,title,names in groups:
 for name in names.split():
  assert name in ts,name
  assert name not in seen,name
  seen.add(name);ts[name]['domain']=gid;ts[name]['manual_create_ddl_read_status']='complete' if ts[name]['create_evidence']['file'] in complete_paths else 'partial_or_pending'
assert set(ts)==seen,set(ts)-seen
c['summary']={db:{'tables':len([t for t in ts.values() if t['database']==db]),'columns':sum(len(t['columns']) for t in ts.values() if t['database']==db),'foreign_key_definitions':sum(con['kind']=='foreign_key' for t in ts.values() if t['database']==db for con in t['constraints']),'explicit_indexes':sum(len(t['indexes']) for t in ts.values() if t['database']==db),'rls_tables':sum(t['rls'] for t in ts.values() if t['database']==db)} for db in ['PostgreSQL','SQLite']}
c['summary']['manual_review']={'complete':len(complete_paths),'partial':sum(m['read_status']=='partial' for m in manifest),'pending':len(manifest)-len(complete_paths)}
(ep/'database-catalog.json').write_text(json.dumps(c,ensure_ascii=False,indent=2),'utf-8')
# 每页保留完整表名及主要键，复杂长字段留到旁置JSON；画图省略不等于否定未绘出的外键。
def key_columns(table, kind):
    return {name for constraint in table['constraints'] if constraint['kind'] == kind for name in constraint['columns']}


def other_foreign_key(name, primary, foreign):
    return name in foreign and name not in primary and name != 'tenant_id'


def selected_columns(columns, primary, foreign):
    special = ['version_no','snapshot_version','version','answer_version','rubric_version','model_version','prompt_version','generation','current_generation','revision','content_hash','is_current','status','state','workflow_status','lifecycle_status','supersedes_id','payload_ciphertext','storage_key']
    keys = [name for name in columns if name in primary]
    keys += [name for name in columns if name in foreign and name not in primary]
    keys += [name for name in special if name in columns]
    keys += [name for name in columns if name not in keys]
    chosen = []
    for name in keys:
        if name in chosen:
            continue
        # 非租户 FK 最多三个，给版本和状态保留展示位置；完整字段仍在旁置目录中。
        if other_foreign_key(name, primary, foreign) and sum(other_foreign_key(item, primary, foreign) for item in chosen) >= 3:
            continue
        chosen.append(name)
        if len(chosen) >= 7:
            break
    return chosen


def unique_constraint_label(table, column_count):
    meaningful = [item for item in table['constraints'] if item['kind'] == 'unique' and item['columns'] != ['tenant_id','id']]
    if meaningful:
        return 'UQ (' + ', '.join(meaningful[0]['columns']) + ')'
    unique_index = next((item for item in table['indexes'] if item['unique']), None)
    if unique_index:
        return 'UQ索引 ' + unique_index['name']
    return '索引 ' + str(len(table['indexes'])) + ' | 字段 ' + str(column_count)


def isolation_label(table):
    if table['rls']:
        return 'RLS已在DDL启用'
    if table['database'] == 'SQLite':
        return '目录隔离；无跨库FK'
    return 'RLS未在DDL启用'


def label(t):
    columns = {column['name']: column for column in t['columns']}
    primary = key_columns(t, 'primary_key')
    foreign = key_columns(t, 'foreign_key')
    lines = [t['name']]
    for name in selected_columns(columns, primary, foreign):
        marks = []
        if name in primary:
            marks.append('PK')
        if name in foreign:
            marks.append('FK')
        suffix = ' [' + ','.join(marks) + ']' if marks else ''
        lines.append(name + ': ' + columns[name]['type'] + suffix)
    lines.append(unique_constraint_label(t, len(columns)))
    lines.append(isolation_label(t))
    return '\n'.join(lines)
def node(n,x,y,w=485,h=340):
 t=ts[n];ev=t['create_evidence'];return {'id':n,'label':label(t),'x':x,'y':y,'w':w,'h':h,'status':'implemented' if ev['file'] in complete_paths else 'unknown','kind':'entity','evidence':ev['file']+':'+str(ev['line'])}
def choose_fk(parent,child):
 fs=[con for con in ts[child]['constraints'] if con['kind']=='foreign_key' and con['referenced_table']==parent]
 return max(fs,key=lambda con:len(con['columns'])) if fs else None
def edge(parent,child,fk,label_override=None):
 cs=fk['columns'];unique=any(con['kind'] in ['primary_key','unique'] and set(con['columns'])<=set(cs) for con in ts[child]['constraints'])
 return {'source':parent,'target':child,'label':label_override or ('1:' + ('0..1' if unique else 'N') +' FK ('+', '.join(cs)+')'),'dashed':False,'evidence':fk['file']+':'+str(fk['line'])}
pages=[]
# 全局入口只展示核心主干。参考页和完整字段目录承接全部表。
gnames=['school','exam','exam_paper','question','student','submission','submission_page','answer_segment','rubric_version','scoring_run','question_grade','review_task','subjective_grading_panel','subjective_grading_run','ai_grade','human_grade','final_grade','score_release','score_release_item','score_release_question']
gpage={'id':'global','title':'数据库全局：核心实体与历史事实','width':2300,'height':2050,'note':'PG 198表 / 桌面SQLite 8表；核心主干仅绘精选FK。其余表见后续27业务域页，完整字段和全部FK见database-catalog.json。未连接部署数据库、未执行迁移。','nodes':[],'edges':[]}
for i,n in enumerate(gnames):gpage['nodes'].append(node(n,55+(i%4)*570,150+(i//4)*365,485,300))
for p,ch in [('school','exam'),('exam','exam_paper'),('exam','submission'),('exam','score_release'),('exam_paper','question'),('student','submission'),('submission','submission_page'),('submission_page','answer_segment'),('question','answer_segment'),('question','rubric_version'),('answer_segment','question_grade'),('scoring_run','question_grade'),('answer_segment','review_task'),('subjective_grading_panel','subjective_grading_run'),('subjective_grading_run','ai_grade'),('review_task','human_grade'),('human_grade','final_grade'),('final_grade','score_release_question'),('score_release','score_release_item'),('score_release','score_release_question')]:
 fk=choose_fk(p,ch)
 if fk:gpage['edges'].append(edge(p,ch,fk))
pages.append(gpage)
# 关联表分别画两条父→子FK，M:N是这两条关系的组合，不能伪造父表之间的直接FK。
relation_page={'id':'cardinality','title':'关系读法：M:N桥表与1:0..1约束','width':2350,'height':2350,'note':'实线父→子只代表已声明FK；M:N由中间表连接。1:0..1表示子表FK受PK/UQ限制，父表可没有子行。部分唯一索引只约束符合WHERE的当前记录，不能画成全历史1:1。','nodes':[],'edges':[]}
relation_rows=[('app_user','user_role','role','M:N：用户与角色'),('role','role_permission','permission','M:N：角色与权限'),('app_user','teacher_class','school_class','M:N：教师用户与班级'),('exam','exam_class','school_class','M:N：考试与班级'),('exam','score_release_current','score_release','每考试至多一个当前发布指针'),('local_asset','upload_session',None,'SQLite：每资产至多一个上传会话')]
for row,(left,middle,right,meaning) in enumerate(relation_rows):
 ids={}
 for column_number,name in enumerate([left,middle,right]):
  if name is None:continue
  item=node(name,55+column_number*775,150+row*355,710,300)
  item['id']='relation_'+str(row)+'_'+name
  if column_number==1:item['label']+='\n'+meaning
  ids[name]=item['id'];relation_page['nodes'].append(item)
 for parent in [left,right]:
  if parent is None:continue
  relation=choose_fk(parent,middle)
  if relation:
   item=edge(parent,middle,relation);item['source']=ids[parent];item['target']=ids[middle];relation_page['edges'].append(item)
pages.append(relation_page)
for gid,title,names in groups:
 ns=names.split();pg={'id':gid,'title':title,'width':2300,'height':1450,'note':'字段/类型/PK/FK依据当前DDL静态重建；绿=人工核对建表DDL，待确认=自动抽取未完整人工审读。全字段、完整FK/唯一约束/索引及增量来源见catalog。箭头父→子；同租户FK不自动等于同学校/考试。','nodes':[],'edges':[]}
 for i,n in enumerate(ns):pg['nodes'].append(node(n,55+(i%4)*570,150+(i//4)*385))
 # 限制为相邻表中的真正FK，避免详页变成连线网；全部关系保存在catalog。
 for i,p in enumerate(ns):
  for j,ch in enumerate(ns):
   if p==ch:continue
   if (j//4==i//4 and abs(j-i)==1) or (j%4==i%4 and abs(j-i)==4):
    fk=choose_fk(p,ch)
    if fk:pg['edges'].append(edge(p,ch,fk))
 if gid=='sqlite':
  pg['note']='独立SQLite 8表，PRAGMA foreign_keys=ON / WAL；不是PG业务实体。目录由server+tenant+actor哈希隔离；载荷/资产AES-GCM加密。远端exam_id/remote_id仅应用层ID，无跨数据库FK。'
  pg['edges']=[]
  for p,ch in [('local_asset','upload_session'),('upload_session','upload_chunk'),('local_asset','local_asset_ingest'),('local_asset','local_asset_chunk'),('local_asset','sync_event')]:pg['edges'].append(edge(p,ch,choose_fk(p,ch)))
 pages.append(pg)
(root/'docs/architecture/specs').mkdir(parents=True,exist_ok=True)
(root/'docs/architecture/specs/04-database-er.json').write_text(json.dumps({'file':'04-database-er.drawio','title':'数据库 ER：PostgreSQL与桌面SQLite','pages':pages},ensure_ascii=False,indent=2),'utf-8')
print(json.dumps({'summary':c['summary'],'diagram_pages':len(pages),'entity_nodes':sum(len(p['nodes']) for p in pages),'manual_review_files':len(manifest)},ensure_ascii=False,indent=2))
