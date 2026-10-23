// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, afterEach, expect, it, vi } from 'vitest';
import { QuestionBankPage } from "./QuestionBankPage";
import type { QuestionBankVersion } from "@edugrade/sdk";
import type { SessionUser } from "../../auth/session";
import type { QuestionBankWorkspaceProps } from "./questionBankWorkspaceTypes";
const mocks=vi.hoisted(()=>({update:vi.fn(),workspace:null as QuestionBankWorkspaceProps | null,success:vi.fn(),error:vi.fn(),confirm:vi.fn()}));
const bank={id:'bank',tenant_id:'tenant',school_id:'school',name:'题库',status:'active',revision:1,metadata_schema_version:1};
const items=['A','B'].map(id=>({id:`item-${id}`,bank_id:'bank',item_code:id,status:'active',revision:1}));
function version(id: string): QuestionBankVersion {return {id:`version-${id}`,tenant_id:'tenant',item_id:`item-${id}`,schema_version:1,revision:1,workflow_status:'draft',stem:`题目${id}`,default_score:3,question_type:'short_answer',assessment_archetype:'constructed_response',options:[],knowledge_points:[],metadata:{},scoring:{answer:{standard_answer:`答案${id}`,equivalent_answers:[],tolerance:{}},solution:null,rubric:null,assets:[],template_version_id:null,use_policy:'practice_only'}} as unknown as QuestionBankVersion}
vi.mock("../../api/questionBank",()=>({questionBankApi:{
 listQuestionBanks:async()=>({banks:[bank],total:1}),listQuestionBankItems:async()=>({items,total:2}),
 listQuestionBankVersions:async({path}: {path: {itemId?: string;versionId?: string}})=>({versions:[version(path.itemId?.endsWith('A')?'A':'B')],total:1}),getQuestionBankVersion:async({path}: {path: {itemId?: string;versionId?: string}})=>({version:version(path.versionId?.endsWith('A')?'A':'B')}),
 getQuestionBankMetadataSchema:async()=>({schema:{bank_id:'bank',version:1,fields:[],taxonomies:[]}}),listQuestionBankReviews:async()=>({reviews:[]}),listQuestionBankMembers:async()=>({members:[]}),
 getQuestionBankItem:async({path}: {path: {itemId?: string;versionId?: string}})=>({item:items.find(i=>i.id===path.itemId)}),getQuestionBank:async()=>({bank}),listQuestionBankRubricTemplates:async()=>({items:[]}),updateQuestionBankScoring:mocks.update
}}));
vi.mock("../../router/useUnsavedChangesGuard",()=>({useUnsavedChangesGuard:()=>undefined}));
vi.mock("./QuestionBankDialogs",()=>({QuestionBankDialogs:()=>null}));
vi.mock("./QuestionBankWorkspace",async()=>{
 // 仅省略展示布局；父页面、评分编辑器和AntD表单真实运行，保留生产的选择与保存回调接线。
 const {ScoringEditor}=await import("./ScoringEditor");
 const {Form,Input}=await import('antd');
 return {QuestionBankWorkspace:(p: QuestionBankWorkspaceProps)=>{mocks.workspace=p;return <><button onClick={()=>p.selectItem(items[0] as QuestionBankWorkspaceProps["item"] & {})}>选A</button><button onClick={()=>p.selectItem(items[1] as QuestionBankWorkspaceProps["item"] & {})}>选B</button><Form form={p.editor}><Form.Item name='stem'><Input/></Form.Item></Form>{p.selected&&p.editorVersion?.id===p.selected.id?<ScoringEditor key={p.selected.id} version={p.editorVersion} disabled={p.contentDirty||p.saving} onSaved={p.scoringSaved} onDirty={p.setScoringDirty} canManageFiles={false} actorId='actor'/>:null}</>}}
});
vi.mock("antd",async importOriginal=>{
 const {withFixedTextAreaLayout}=await import("../../test-utils/antdTextAreaLayout");
 return {...withFixedTextAreaLayout(await importOriginal<typeof import("antd")>()),App:{useApp:()=>({message:{success:mocks.success,error:mocks.error,warning:vi.fn()},modal:{confirm:mocks.confirm}})}};
});
let host: HTMLDivElement, root: Root, client: QueryClient;
beforeEach(()=>{vi.clearAllMocks();mocks.workspace=null;location.hash='';Object.assign(globalThis,{IS_REACT_ACT_ENVIRONMENT:true});vi.stubGlobal('matchMedia',()=>({matches:false,addListener(){},removeListener(){},addEventListener(){},removeEventListener(){}}));vi.stubGlobal('ResizeObserver',class{observe(){}unobserve(){}disconnect(){}});const original=window.getComputedStyle.bind(window);vi.spyOn(window,'getComputedStyle').mockImplementation(e=>original(e));host=document.createElement('div');document.body.append(host);root=createRoot(host);client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}})});
afterEach(async()=>{await act(async()=>root.unmount());client.clear();host.remove();vi.unstubAllGlobals();vi.restoreAllMocks()});
async function flush(){await act(async()=>{await new Promise(r=>setTimeout(r,20))})}
async function waitFor(ready: () => boolean){for(let i=0;i<30&&!ready();i++)await flush();expect(ready()).toBe(true)}
async function click(text: string){const b=[...host.querySelectorAll('button')].find(e=>e.textContent?.replace(/\s/g,'')===text);expect(b).toBeDefined();await act(async()=>b!.click());await flush()}
const user={id:'actor',tenant:'tenant',roles:[],permissions:['question_bank:read','question_bank:edit'],organizationScope:{schoolIds:['school'],gradeIds:[],classIds:[],resolved:true,tenantWide:false}} as unknown as SessionUser;
it.each([true,false])('评分保存A回执晚于选B=%s的真实父子边界',async late=>{
 let release!: (value: { version: QuestionBankVersion }) => void;const result={version:{...version('A'),revision:2}};
 mocks.update.mockImplementation(async()=>late?await new Promise<{ version: QuestionBankVersion }>(r=>release=r):result);
 await act(async()=>root.render(<QueryClientProvider client={client}><QuestionBankPage user={user}/></QueryClientProvider>));await flush();await click('选A');await waitFor(()=>mocks.workspace?.editorVersion?.id==='version-A');
 await click('保存评分标准');await waitFor(()=>mocks.update.mock.calls.length===1);
 await click('选B');await waitFor(()=>mocks.workspace?.editorVersion?.id==='version-B');
 expect(mocks.workspace!.selected!.id).toBe('version-B');expect(mocks.workspace!.editor.getFieldValue('stem')).toBe('题目B');
 if(late){await act(async()=>release(result));await flush();}
 expect(mocks.workspace!.selected!.id).toBe('version-B');expect(mocks.workspace!.editorVersion!.id).toBe('version-B');expect(mocks.workspace!.editor.getFieldValue('stem')).toBe('题目B');
});
