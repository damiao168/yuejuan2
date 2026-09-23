import { Alert, Button, Form, Input, InputNumber, Modal, Select, Switch } from "antd";
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

type QueryView<T> = Pick<UseQueryResult<T, Error>, "data" | "isError" | "isLoading" | "error" | "refetch">;

type QuestionBankDialogsProps = {
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
  schemaOpen: boolean;
  setSchemaOpen: (open: boolean) => void;
  currentSchema: QueryView<QuestionBankMetadataSchemaResponse>;
  schemaForm: FormInstance;
  saveSchema: (values: MetadataSchemaFormValues) => void;
  searchOpen: boolean;
  setSearchOpen: (open: boolean) => void;
  searchFilters: SearchFilters;
  setSearchFilters: Dispatch<SetStateAction<SearchFilters>>;
  setSearchOffset: (offset: number) => void;
  searchOffset: number;
  searchResults: QueryView<QuestionBankSearchPage>;
  selectItem: (item: QuestionBankItem) => void;
  setVersionId: (id: string) => void;
  kind: "question" | "rubric_template";
  createItem: (values: CreateQuestionBankItemRequest) => void;
  defaults: CreateQuestionBankItemRequest;
};

export function QuestionBankDialogs(props: QuestionBankDialogsProps) {
  const {
    dialog, setDialog, saving, schoolOptions, schools, createBank, aclOpen,
    setAclOpen, aclForm, saveACL, acl, schemaOpen, setSchemaOpen, currentSchema,
    schemaForm, saveSchema, searchOpen, setSearchOpen, searchFilters,
    setSearchFilters, setSearchOffset, searchOffset, searchResults, selectItem,
    setVersionId, kind, createItem, defaults,
  } = props;
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
				<Alert type="info" showIcon message="Manager 只管理结构与授权，不自动获得题干浏览权限。用户和组预设由后端展开，每次请求重新解析。" />
				<Form form={aclForm} layout="vertical" onFinish={saveACL} disabled={saving || acl.isLoading}>
					<h3>用户授权</h3>
					<Form.List name="bindings">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-rule-row" key={key}>
								<Form.Item {...field} name={[field.name,"user_id"]} rules={[{required:true}]}><Input placeholder="用户 UUID" /></Form.Item>
								<Form.Item {...field} name={[field.name,"preset"]} rules={[{required:true}]}><Select options={["Viewer","Author","Reviewer","Publisher","Manager"].map(value=>({value,label:value}))} /></Form.Item>
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
								<Form.Item {...field} name={[field.name,"preset"]} label="预设" rules={[{required:true}]}><Select options={["Viewer","Author","Reviewer","Publisher","Manager"].map(value=>({value,label:value}))} /></Form.Item>
								<Form.Item {...field} name={[field.name,"members"]} label="成员 UUID（每行一个）"><Input.TextArea rows={3} /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除组</Button>
							</div>)}
							<Button onClick={() => add({preset:"Author"})}>添加组</Button>
						</>}
					</Form.List>
					<Button type="primary" htmlType="submit" loading={saving}>保存完整 ACL</Button>
				</Form>
			</Modal>
			<Modal title={`Metadata 规则 · v${currentSchema.data?.schema.version ?? "—"}`} open={schemaOpen} onCancel={() => setSchemaOpen(false)} footer={null} width={900} destroyOnHidden>
				<Alert type="info" showIcon message="保存会创建新 schema 版本；旧题目继续按原版本解释，稳定 option 与 taxonomy ID 会保留在历史定义中。" />
				<Form form={schemaForm} layout="vertical" onFinish={saveSchema} disabled={saving || currentSchema.isLoading}>
					<Form.List name="fields">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-schema-row" key={key}>
								<Form.Item {...field} name={[field.name,"key"]} label="字段 key" rules={[{required:true}]}><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"label"]} label="显示名" rules={[{required:true}]}><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"type"]} label="类型" rules={[{required:true}]}><Select options={["enum","string","number","boolean","taxonomy"].map(value=>({value,label:value}))} /></Form.Item>
								<Form.Item {...field} name={[field.name,"required"]} label="必填" valuePropName="checked"><Switch /></Form.Item>
								<Form.Item {...field} name={[field.name,"min"]} label="最小值"><InputNumber /></Form.Item>
								<Form.Item {...field} name={[field.name,"max"]} label="最大值"><InputNumber /></Form.Item>
								<Form.Item {...field} name={[field.name,"max_length"]} label="最大长度"><InputNumber min={1} max={2000} /></Form.Item>
								<Form.Item {...field} name={[field.name,"taxonomy_id"]} label="Taxonomy ID"><Input /></Form.Item>
								<Form.Item {...field} name={[field.name,"options_text"]} label="枚举项 value|标签（每行一个）"><Input.TextArea rows={3} /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除字段</Button>
							</div>)}
							<Button onClick={() => add({type:"string",required:false,max_length:120})}>添加字段</Button>
						</>}
					</Form.List>
					<h3>Taxonomies</h3>
					<Form.List name="taxonomies">
						{(fields, { add, remove }) => <>
							{fields.map(({ key, ...field }) => <div className="question-bank-rule-row" key={key}>
								<Form.Item {...field} name={[field.name,"id"]} rules={[{required:true}]}><Input placeholder="taxonomy_id" /></Form.Item>
								<Form.Item {...field} name={[field.name,"label"]} rules={[{required:true}]}><Input placeholder="显示名" /></Form.Item>
								<Form.Item {...field} name={[field.name,"terms_text"]} rules={[{required:true}]}><Input.TextArea rows={3} placeholder="term_id|标签，每行一个" /></Form.Item>
								<Button danger onClick={() => remove(field.name)}>移除</Button>
							</div>)}
							<Button onClick={() => add()}>添加 taxonomy</Button>
						</>}
					</Form.List>
					<Button type="primary" htmlType="submit" loading={saving}>发布新 schema 版本</Button>
				</Form>
			</Modal>
			<Modal title="组合检索" open={searchOpen} onCancel={() => setSearchOpen(false)} footer={null} width={980} destroyOnHidden>
				<div className="question-bank-filter-grid">
					<Input.Search value={searchFilters.q} placeholder="题干或题目编号" onChange={event=>setSearchFilters(current=>({...current,q:event.target.value}))} onSearch={()=>{setSearchOffset(0);void searchResults.refetch();}} />
					<Select allowClear placeholder="学科" value={searchFilters.subject_code} options={subjects} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,subject_code:value}))}} />
					<Select allowClear placeholder="题型" value={searchFilters.question_type} options={types} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,question_type:value}))}} />
					<Select allowClear placeholder="版本状态" value={searchFilters.workflow_status} options={Object.entries(workflowLabels).map(([value,label])=>({value,label}))} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,workflow_status:value}))}} />
					<Select allowClear placeholder="难度标签" value={searchFilters.difficulty_band} options={["easy","medium","hard"].map(value=>({value,label:value}))} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,difficulty_band:value}))}} />
					<Input placeholder="知识点 ID" value={searchFilters.knowledge_point} onChange={event=>{setSearchOffset(0);setSearchFilters(current=>({...current,knowledge_point:event.target.value}))}} />
					<Select value={searchFilters.mode} options={[{value:"default",label:"已发布 + 我的草稿"},{value:"published",label:"当前发布版"},{value:"my_drafts",label:"我的草稿"},{value:"all",label:"全部可见版本"}]} onChange={value=>{setSearchOffset(0);setSearchFilters(current=>({...current,mode:value}))}} />
				</div>
				<ResponsiveTable<QuestionBankSearchItem> className="dense-data-table" rowKey={record=>record.version.id} loading={searchResults.isLoading} dataSource={searchResults.data?.items ?? []} columns={[
					{title:"编号",render:(_,record)=><Button type="link" onClick={()=>{selectItem(record.item);setVersionId(record.version.id);setSearchOpen(false)}}>{record.item.item_code}</Button>},
					{title:"题干",dataIndex:["version","stem"],ellipsis:true},
					{title:"版本",render:(_,record)=>`v${record.version.version_no} · ${workflowLabels[record.version.workflow_status]}`},
					{title:"难度",render:(_,record)=>record.version.metadata.difficulty_band},
					{title:"统计",render:(_,record)=>record.statistics_available?"已有观测":"尚无观测"},
				]} pagination={{current:searchOffset/20+1,pageSize:20,total:searchResults.data?.total,showSizeChanger:false,onChange:page=>setSearchOffset((page-1)*20)}} />
			</Modal>
			<Modal
				title={
					kind === "rubric_template" ? "新建 Rubric 模板草稿" : "新建题目草稿"
				}
				open={dialog === "item"}
				onCancel={() => !saving && setDialog(null)}
				footer={null}
				width={680}
				destroyOnHidden
			>
				<Form
					layout="vertical"
					onFinish={createItem}
					initialValues={defaults}
					disabled={saving}
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

