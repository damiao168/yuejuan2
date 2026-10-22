import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import ts from "typescript";
import { createBreakingBaseline, generateTypes } from "../lib/openapi-sdk.mjs";
import { findBreakingChanges } from "../check-openapi-breaking.mjs";

const spec=JSON.parse(fs.readFileSync("services/api-gateway/openapi/edugrade-api.openapi.json","utf8"));
function compile(t,types,fixture){
  const directory=fs.mkdtempSync(path.join(os.tmpdir(),"sdk-contract-"));
  t.after(()=>fs.rmSync(directory,{recursive:true,force:true}));
  fs.writeFileSync(path.join(directory,"types.ts"),types);
  const file=path.join(directory,"consumer.ts");
  fs.writeFileSync(file,fixture);
  // 此夹具只编译生成类型和消费端；不自动引入仓库依赖的环境类型，避免污染契约诊断。
  const program=ts.createProgram([file],{noEmit:true,strict:true,types:[],module:ts.ModuleKind.ESNext,moduleResolution:ts.ModuleResolutionKind.Bundler,target:ts.ScriptTarget.ES2022});
  const diagnostics=ts.getPreEmitDiagnostics(program);
  assert.deepEqual(diagnostics.map(item=>ts.flattenDiagnosticMessageText(item.messageText,"\n")),[]);
}

test("actual SDK allows nonempty typed maps and rejects incorrect values",t=>{
  compile(t,generateTypes(spec),`
import type { ErrorResponse, ExamWorkspaceSubjectSummary, ReviewTaskAggregate } from './types';
const errors: NonNullable<ErrorResponse['field_errors']> = {score:['需要数字']};
const questions: ExamWorkspaceSubjectSummary['question_types'] = {choice:2};
const statuses: ReviewTaskAggregate['status_counts'] = {pending:1};
// @ts-expect-error 字典值必须保留源契约的string[]约束。
const invalid: NonNullable<ErrorResponse['field_errors']> = {score:12};
`);
});

test("actual import body preserves subject and at least one source branch",t=>{
  compile(t,generateTypes(spec),`
import type { CreatePaperImportRequest } from './types';
const valid: CreatePaperImportRequest = {subject:'chinese',paper_file_asset_id:'synthetic-id'};
// @ts-expect-error 缺少至少一种来源。
const absentSource: CreatePaperImportRequest = {subject:'chinese'};
// @ts-expect-error 有来源仍必须给出主体科目。
const absentSubject: CreatePaperImportRequest = {paper_file_asset_id:'synthetic-id'};
// @ts-expect-error 任意字符串不是合法对象正文。
const scalar: CreatePaperImportRequest = 'incorrect';
`);
});

test("schema compositions retain sibling fields and required-only inherited types",t=>{
  const schemas={ Combined:{type:'object',required:['subject'],properties:{subject:{type:'string'},a:{type:'number'},b:{type:'boolean'}},anyOf:[{required:['a']},{required:['b']}],allOf:[{type:'object',required:['revision'],properties:{revision:{type:'integer'}}}]} };
  compile(t,generateTypes({components:{schemas}}),`
import type { Combined } from './types';
const valid: Combined = {subject:'math',a:1,revision:1};
// @ts-expect-error anyOf与主体、allOf一起成立，不能省略来源和revision。
const absent: Combined = {subject:'math'};
// @ts-expect-error required-only分支必须沿用a的数字类型。
const wrong: Combined = {subject:'math',a:'wrong',revision:1};
`);
});

test("full baseline catches actual field type/removal and nested shape changes",()=>{
  const baseline=createBreakingBaseline(spec);
  assert.equal(Object.keys(baseline.schemas.SubjectiveAIGrade.properties).length,Object.keys(spec.components.schemas.SubjectiveAIGrade.properties).length);
  for(const mutate of [
    current=>{current.components.schemas.SubjectiveAIGrade.properties.suggested_score.type='string';},
    current=>{delete current.components.schemas.SubjectiveAIGrade.properties.failure_reason;},
  ]){
    const current=structuredClone(spec);mutate(current);
    assert.ok(findBreakingChanges(baseline,createBreakingBaseline(current)).length>0);
  }
  const before={paths:{},components:{schemas:{Nested:{type:'object',properties:{child:{type:'object',properties:{value:{type:'number'}}}}}}}};
  const after=structuredClone(before);after.components.schemas.Nested.properties.child.properties.value.type='string';
  assert.ok(findBreakingChanges(createBreakingBaseline(before),createBreakingBaseline(after)).some(value=>value.includes('Nested.child.value')));
  const addition=structuredClone(before);addition.components.schemas.Nested.properties.optional={type:'boolean'};
  assert.deepEqual(findBreakingChanges(createBreakingBaseline(before),createBreakingBaseline(addition)),[]);
  assert.deepEqual(findBreakingChanges(baseline,createBreakingBaseline(spec)),[]);
});

test("baseline retains keyword-like property names and all supported constraints",()=>{
  const source={paths:{},components:{schemas:{Constrained:{type:'object',properties:{type:{type:['string','null'],format:'uuid',nullable:true,const:'fixed',enum:['fixed'],additionalProperties:{type:'number'},anyOf:[{required:['a']}],oneOf:[{required:['b']}],allOf:[{required:['c']}]},enum:{type:'number'}}}}}};
  const baseline=createBreakingBaseline(source);
  assert.deepEqual(baseline.schemas.Constrained.properties.enum,{type:'number'});
  assert.deepEqual(findBreakingChanges(baseline,createBreakingBaseline(structuredClone(source))),[]);
  for(const key of ['format','nullable','const','enum','additionalProperties','anyOf','oneOf','allOf']){
    const candidate=structuredClone(source);
    delete candidate.components.schemas.Constrained.properties.type[key];
    assert.ok(findBreakingChanges(baseline,createBreakingBaseline(candidate)).length>0,key);
  }
  const literal={paths:{},components:{schemas:{Literal:{enum:[{kind:'fixed'}]}}}};
  assert.deepEqual(findBreakingChanges(createBreakingBaseline(literal),createBreakingBaseline(structuredClone(literal))),[]);
});

test("breaking gate rejects removal of an existing request schema",()=>{
  const source={paths:{'/synthetic':{post:{operationId:'synthetic',requestBody:{content:{'application/json':{schema:{type:'object',properties:{score:{type:'number'}}}}}}}}}};
  const candidate=structuredClone(source);
  delete candidate.paths['/synthetic'].post.requestBody.content['application/json'].schema;
  assert.ok(findBreakingChanges(createBreakingBaseline(source),createBreakingBaseline(candidate)).some(value=>value.includes('request')));
});
