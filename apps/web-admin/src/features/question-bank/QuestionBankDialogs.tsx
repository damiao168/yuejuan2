import { Alert, Button, Form, Input, Modal, Select } from "antd";
import type { FormInstance } from "antd";
import type { UseQueryResult } from "@tanstack/react-query";
import type { Dispatch, SetStateAction } from "react";
import type {
  CreateQuestionBankItemRequest,
  CreateQuestionBankRequest,
  QuestionBankACLResponse,
  QuestionBankItem,
  QuestionBankMetadataSchemaResponse,
  QuestionBankSearchItem,
  QuestionBankSearchPage,
} from "@edugrade/sdk";
import type { School } from "../../api/org";
import { ResponsiveTable } from "../../components/ResponsiveTable";
import { ContentFields } from "./ContentFields";
import { subjects, types } from "./questionBankOptions";
import { workflowLabels, type ACLFormValues, type MetadataSchemaFormValues, type SearchFilters } from "./questionBankTypes";
import { bankRoleOptions, questionBankMemberAmbiguous, questionBankMemberLabel, type QuestionBankMember } from "./questionBankMembers";
import { QuestionBankClassificationFields } from "./QuestionBankClassificationFields";

type QueryView<T> = Pick<UseQueryResult<T, Error>, "data" | "isError" | "isLoading" | "error" | "refetch">;
const aclActionLabels: Record<string, string> = { read: "浏览题目", create: "新建题目", edit: "编辑草稿", review: "审核题目", publish: "发布题目", retire: "停止选用", statistics: "查看统计", manage: "管理结构与授权" };
const difficultyLabels: Record<string, string> = { easy: "容易", medium: "中等", hard: "较难", unclassified: "尚未标注" };

type QuestionBankDialogsProps = Readonly<{
  bankId: string;
  dialog: "bank" | "item" | null;
  setDialog: (dialog: "bank" | "item" | null) => void;
  saving: boolean;
  schoolOptions: Array<{ value: string; label: string }>;
  schools: QueryView<{ schools: School[] }>;
  createBank: (values: CreateQuestionBankRequest) => void;
  aclOpen: boolean;
  setAclOpen: (open: boolean) => void;
  aclForm: FormInstance;
  saveACL: (values: ACLFormValues) => void;
  acl: QueryView<QuestionBankACLResponse>;
  members?: QueryView<{members:QuestionBankMember[]}>;
  protectedMemberIds?: string[];
  schemaOpen: boolean;
  setSchemaOpen: (open: boolean) => void;
  currentSchema: QueryView<QuestionBankMetadataSchemaResponse>;
  schemaReadable: boolean;
  schemaFormReady: boolean;
  schemaLoadFailed: boolean;
  schemaForm: FormInstance;
  saveSchema: (values: MetadataSchemaFormValues) => void;
  searchOpen: boolean;
  setSearchOpen: (open: boolean) => void;
  searchFilters: SearchFilters;
  setSearchFilters: Dispatch<SetStateAction<SearchFilters>>;
  setSearchOffset: (offset: number) => void;
  searchOffset: number;
  searchResults: QueryView<QuestionBankSearchPage>;
  selectItem: (item: QuestionBankItem, versionId?: string, onSelected?: () => void) => void;
  kind: "question" | "rubric_template";
  createItem: (values: CreateQuestionBankItemRequest) => void;
  defaults: Omit<CreateQuestionBankItemRequest, "metadata"> & {metadata: Partial<CreateQuestionBankItemRequest["metadata"]>};
}>;

export function QuestionBankDialogs(props: QuestionBankDialogsProps) {
  const {
    bankId, dialog, setDialog, saving, schoolOptions, schools, createBank, aclOpen,
    setAclOpen, aclForm, saveACL, acl, schemaOpen, setSchemaOpen, currentSchema, schemaReadable, schemaFormReady, schemaLoadFailed,
    schemaForm, saveSchema, searchOpen, setSearchOpen, searchFilters,
    setSearchFilters, setSearchOffset, searchOffset, searchResults, selectItem,
    kind, createItem, defaults, members, protectedMemberIds = [],
  } = props;
  const selectedIds = [...(acl.data?.acl.bindings.map(binding=>binding.user_id) ?? []), ...(acl.data?.acl.groups.flatMap(group=>group.member_user_ids) ?? [])];
  const directory = members?.data?.members ?? [];
  const ambiguousMembers = directory.filter(person => questionBankMemberAmbiguous(person, directory));
  const memberOptions = [...directory.map(person=>({value:person.user_id,label:questionBankMemberLabel(person),disabled:questionBankMemberAmbiguous(person, directory)})), ...selectedIds.filter((id,index)=>selectedIds.indexOf(id)===index && !directory.some(person=>person.user_id===id)).map(id=>({value:id,label:"已有授权成员（姓名暂不可读取）"}))];
  const retainedBindings = acl.data?.acl.bindings.filter(binding => binding.preset === "Custom" || protectedMemberIds.includes(binding.user_id)) ?? [];
  const retainedGroups = acl.data?.acl.groups.filter(group => String(group.preset) === "Custom") ?? [];
  const memberName = (id: string) => memberOptions.find(person => person.value === id)?.label ?? "已有授权成员（姓名暂不可读取）";
  const assignableMembers = memberOptions.filter(person => !retainedBindings.some(binding => binding.user_id === person.value));
  // 检索可查历史停用知识点；提交仍用稳定编号，教师只按名称选择。
  const knowledgeTerms = currentSchema.data?.schema.taxonomies.find(taxonomy => taxonomy.id === "knowledge_points")?.terms ?? [];
  const knowledgeOptions = knowledgeTerms.map(term=>({value:term.id,label:`${term.label}${term.active ? "" : "（已停用，可查历史）"}`}));
  if (searchFilters.knowledge_point && !knowledgeTerms.some(term => term.id === searchFilters.knowledge_point)) knowledgeOptions.push({value:searchFilters.knowledge_point,label:"原选知识点（当前目录已不包含，可清除）"});
  const emptyKnowledgeNotice = !schemaLoadFailed && schemaReadable && !knowledgeTerms.length
    ? <p>当前题库尚未配置知识点目录，请联系题库负责人维护；仍可使用其他条件检索。</p>
    : null;
  return (
    <>			<Modal
				title="新建题库"
				open={dialog === "bank"}
				onCancel={() => !saving && setDialog(null)}
				footer={null}
				destroyOnHidden
			>
				<Form
					layout="vertical"
					onFinish={createBank}
					initialValues={{
						school_id: schoolOptions[0]?.value,
						description: "",
					}}
				>
					<Form.Item
						name="school_id"
						label="所属学校"
						rules={[{ required: true }]}
					>
						<Select options={schoolOptions} loading={schools.isLoading} />
					</Form.Item>
					{schools.isError && <Alert type="error" message="学校列表加载失败" />}
					<Form.Item
						name="name"
						label="题库名称"
						rules={[{ required: true, whitespace: true, max: 160 }]}
					>
						<Input maxLength={160} />
					</Form.Item>
					<Form.Item name="description" label="说明">
						<Input.TextArea maxLength={2000} rows={3} />
					</Form.Item>
					<Button type="primary" htmlType="submit" loading={saving}>
						创建题库
					</Button>
				</Form>
			</Modal>
			<Modal title="访问权限" open={aclOpen} onCancel={() => setAclOpen(false)} footer={null} width={820} destroyOnHidden>
				<Alert type="info" showIcon message="按实际职责授予权限" description="浏览可看题；出题可编辑草稿；审核可批准其他教师的版本；发布可将已批准版本用于题库；管理仅维护结构与授权，不自动获得看题权限。" />
				{members?.isError ? <Alert type="warning" message="人员列表暂不可读取，已有授权仍保留" action={<Button onClick={()=>void members.refetch()}>重试人员列表</Button>} /> : null}
                {ambiguousMembers.length > 0 && <Alert type="warning" showIcon message="部分同名人员缺少可区分的账号，暂不能选用" description="请重新读取人员列表；已有授权继续保留，确认身份后再调整。" action={<Button onClick={() => void members?.refetch()}>重新读取人员列表</Button>} />}
                {retainedBindings.some(binding => binding.preset === "Custom") || retainedGroups.length ? <Alert type="info" showIcon message="历史组合权限只读保留" description="以下历史组合授权由服务端原样保留。可继续调整其他人员与普通题库组的预设职责；本次更新不会删改这些组合权限。" /> : null}
                {retainedBindings.length > 0 && <section aria-label="只读保留的用户授权"><h3>只读保留的用户授权</h3><ul>{retainedBindings.map(binding => <li key={binding.user_id}><strong>{memberName(binding.user_id)}</strong> · {binding.preset === "Custom" ? "历史组合权限" : "受保护用户"}：{binding.actions.map(action => aclActionLabels[action] ?? "既有权限").join("、") || "既有权限"}</li>)}</ul><p>创建者和当前操作者的直接授权也由服务端保留。</p></section>}
                {retainedGroups.length > 0 && <section aria-label="只读保留的题库组"><h3>只读保留的题库组</h3><ul>{retainedGroups.map(group => <li key={group.id}><strong>{group.name}</strong>：{group.actions.map(action => aclActionLabels[action] ?? "既有权限").join("、")}；组内人员：{group.member_user_ids.map(memberName).join("、") || "暂无人员"}</li>)}</ul></section>}
                {acl.isError ? <Alert type="error" message="当前授权读取失败，不能据空列表覆盖原授权" action={<Button onClick={()=>void acl.refetch()}>重新读取授权</Button>} /> : null}
				<Form form={aclForm} layout="vertical" onFinish={saveACL} disabled={saving || acl.isLoading || acl.isError}>
					<h3>用户授权</h3>
					<Form.List name="bindings">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-rule-row" key={key}>
								<Form.Item {...field} name={[field.name,"user_id"]} rules={[{required:true}]}><Select showSearch optionFilterProp="label" loading={members?.isLoading} options={assignableMembers} placeholder="按姓名或账号选择本校人员" /></Form.Item>
								<Form.Item {...field} name={[field.name,"preset"]} rules={[{required:true}]}><Select options={bankRoleOptions} /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除</Button>
							</div>)}
							<Button onClick={() => add({preset:"Viewer"})}>添加用户</Button>
						</>}
					</Form.List>
					<h3>题库组</h3>
					<Form.List name="groups">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-group-row" key={key}>
								<Form.Item {...field} name={[field.name,"id"]} hidden><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"name"]} label="组名" rules={[{required:true}]}><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"preset"]} label="职责权限" rules={[{required:true}]}><Select options={bankRoleOptions} /></Form.Item>
									<Form.Item {...field} name={[field.name,"members"]} label="组内人员"><Select mode="multiple" showSearch optionFilterProp="label" loading={members?.isLoading} options={memberOptions} placeholder="按姓名或账号选择组内人员" /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除组</Button>
							</div>)}
							<Button onClick={() => add({preset:"Author"})}>添加组</Button>
						</>}
					</Form.List>
					<Button type="primary" htmlType="submit" loading={saving}>核对并更新授权</Button>
				</Form>
			</Modal>
			<Modal title={`题目分类与知识点 · 第 ${currentSchema.data?.schema.version ?? "—"} 版`} open={schemaOpen} onCancel={() => !saving && setSchemaOpen(false)} footer={null} width={900} destroyOnHidden>
                <Alert type="info" showIcon message="更新规则会另存一版，已有题目继续使用原规则" description="先建立知识点或分类目录，再按需要添加补充字段。内部编号自动生成，无需填写。" />
                {schemaLoadFailed ? <Alert type="error" showIcon message="当前分类规则读取失败，暂不能保存修改" description="读取失败不代表题库没有配置。请重新读取，成功后再编辑或保存。" action={<Button onClick={() => void currentSchema.refetch()}>重新读取分类规则</Button>} /> : null}
                <Form form={schemaForm} layout="vertical" onFinish={saveSchema} disabled={saving || !schemaFormReady}>
                    <QuestionBankClassificationFields form={schemaForm} />
                    <Button type="primary" htmlType="submit" loading={saving}>保存新的分类规则</Button>
                </Form>
            </Modal>
            <Modal title="组合检索" open={searchOpen} onCancel={() => setSearchOpen(false)} footer={null} width={980} destroyOnHidden>
				<div className="question-bank-filter-grid">
					<Input.Search value={searchFilters.q} placeholder="题干或题目编号" onChange={event=>setSearchFilters(current=>({...current,q:event.target.value}))} onSearch={()=>{setSearchOffset(0);void searchResults.refetch();}} />
					<Select allowClear placeholder="学科" value={searchFilters.subject_code} options={subjects} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,subject_code:value}))}} />
					<Select allowClear placeholder="题型" value={searchFilters.question_type} options={types} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,question_type:value}))}} />
					<Select allowClear placeholder="版本状态" value={searchFilters.workflow_status} options={Object.entries(workflowLabels).map(([value,label])=>({value,label}))} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,workflow_status:value}))}} />
					<Select aria-label="检索难度" allowClear placeholder="难度标签" value={searchFilters.difficulty_band} options={[{value:"easy",label:"容易"},{value:"medium",label:"中等"},{value:"hard",label:"较难"}]} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,difficulty_band:value}))}} />
					<Select aria-label="检索知识点" allowClear showSearch optionFilterProp="label" placeholder="按名称选择知识点" loading={currentSchema.isLoading} disabled={!schemaReadable || (!knowledgeTerms.length && !searchFilters.knowledge_point)} value={searchFilters.knowledge_point} options={knowledgeOptions} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,knowledge_point:value}))}} />
					<Select value={searchFilters.mode} options={[{value:"default",label:"已发布 + 我的草稿"},{value:"published",label:"当前发布版"},{value:"my_drafts",label:"我的草稿"},{value:"all",label:"全部可见版本"}]} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,mode:value}))}} />
				</div>
				{schemaLoadFailed ? <Alert type="warning" message="知识点目录读取失败" action={<Button onClick={()=>void currentSchema.refetch()}>重试知识点目录</Button>} /> : emptyKnowledgeNotice}
				<ResponsiveTable<QuestionBankSearchItem> className="dense-data-table" rowKey={record=>record.version.id} loading={searchResults.isLoading} dataSource={searchResults.data?.items ?? []} columns={[
					{title:"编号",render:(_,record)=><Button type="link" onClick={()=>selectItem(record.item,record.version.id,()=>setSearchOpen(false))}>{record.item.item_code}</Button>},
					{title:"题干",dataIndex:["version","stem"],ellipsis:true},
					{title:"版本",render:(_,record)=>`v${record.version.version_no} · ${workflowLabels[record.version.workflow_status]}`},
					{title:"难度",render:(_,record)=>difficultyLabels[record.version.metadata.difficulty_band] ?? "其他难度标签"},
					{title:"统计",render:(_,record)=>record.statistics_available?"已有观测":"尚无观测"},
				]} pagination={{current:searchOffset/20+1,pageSize:20,total:searchResults.data?.total,showSizeChanger:false,onChange:page=>setSearchOffset((page-1)*20)}} />
			</Modal>
			<Modal
				title={
					kind === "rubric_template" ? "新建评分标准模板草稿" : "新建题目草稿"
				}
				open={dialog === "item"}
				onCancel={() => !saving && setDialog(null)}
				footer={null}
				width={680}
				destroyOnHidden
			>
				{schemaLoadFailed ? <Alert type="error" showIcon message="当前分类规则读取失败，暂不能创建题目草稿" description="重新读取成功后，才能核对当前题库要求的字段与知识点。" action={<Button onClick={() => void currentSchema.refetch()}>重新读取分类规则</Button>} /> : null}
				{/* 新建草稿的填写内容只属于当前题库；切库后重建表单，避免沿用上一库的字段。 */}
				<Form
					key={bankId}
					layout="vertical"
					onFinish={createItem}
					initialValues={defaults}
					disabled={saving || !schemaReadable}
				>
					<Form.Item
						name="item_code"
						label="题目编号"
						rules={[
							{
								required: true,
								pattern: /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/,
								message: "使用字母、数字、点、下划线或短横线，最多64个字符",
							},
						]}
					>
						<Input />
					</Form.Item>
					<ContentFields schema={currentSchema.data?.schema} />
					<Button type="primary" htmlType="submit" loading={saving}>
						{kind === "rubric_template" ? "创建模板草稿" : "创建题目草稿"}
					</Button>
				</Form>
			</Modal>
    </>
  );
}
