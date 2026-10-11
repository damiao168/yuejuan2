import { Button, Form, Input, InputNumber, Select, Switch, type FormInstance } from "antd";

const newCode = (kind: string) => `${kind}_${crypto.randomUUID().replace(/-/g, "")}`;
const fieldTypes = [{value:"string",label:"文字"},{value:"number",label:"数值"},{value:"boolean",label:"是或否"},{value:"enum",label:"选项"},{value:"taxonomy",label:"目录分类"}];

function ClassificationEnumOptions({ fieldName }: Readonly<{ fieldName: number }>) {
 // 相对路径沿用外层字段列表的位置；改名称或停用选项时不能重建历史内部编号。
 return <Form.List name={[fieldName,"options"]}>{(options,ops)=><><h4>可选内容</h4>{options.map(option=><div key={option.key} className="question-bank-form-pair"><Form.Item name={[option.name,"value"]} hidden><Input /></Form.Item><Form.Item name={[option.name,"label"]} label="选项名称" rules={[{required:true}]}><Input /></Form.Item><Form.Item name={[option.name,"active"]} label="允许新题目选择" valuePropName="checked"><Switch /></Form.Item></div>)}<Button onClick={()=>ops.add({value:newCode("option"),label:"",active:true})}>添加选项</Button></>}</Form.List>;
}

export function QuestionBankClassificationFields({form}:Readonly<{form:FormInstance}>) {
 const taxonomies=Form.useWatch("taxonomies",form) ?? [];
 return <>
  <h3>题目补充字段</h3><p>填写显示名称和内容类型，系统生成内部编号。已有编号保持不变，以便历史题目继续使用。</p>
  <Form.List name="fields">{(fields,{add,remove})=><>{fields.map(field=><section key={field.key} className="question-bank-schema-row">
   <Form.Item name={[field.name,"key"]} hidden><Input /></Form.Item>
   <Form.Item name={[field.name,"label"]} label="字段名称" rules={[{required:true}]}><Input placeholder="例如：所属教材" /></Form.Item>
   <Form.Item name={[field.name,"type"]} label="内容类型" rules={[{required:true}]}><Select options={fieldTypes} /></Form.Item>
   <Form.Item name={[field.name,"required"]} label="教师必须填写" valuePropName="checked"><Switch /></Form.Item>
   <Form.Item noStyle shouldUpdate>{()=>{
    const type=form.getFieldValue(["fields",field.name,"type"]);
    return <>
     {type==="number" ? <div className="question-bank-form-pair"><Form.Item name={[field.name,"min"]} label="最小值"><InputNumber /></Form.Item><Form.Item name={[field.name,"max"]} label="最大值"><InputNumber /></Form.Item></div> : null}
     {type==="string" ? <Form.Item name={[field.name,"max_length"]} label="最多填写字数"><InputNumber min={1} max={2000}/></Form.Item> : null}
     {type==="taxonomy" ? <Form.Item name={[field.name,"taxonomy_id"]} label="使用哪个目录" rules={[{required:true}]}><Select options={taxonomies.map((item:{id:string;label:string})=>({value:item.id,label:item.label}))} placeholder="先在下方建立目录，再选择" /></Form.Item> : null}
     {type==="enum" ? <ClassificationEnumOptions fieldName={field.name} /> : null}
    </>;
   }}</Form.Item>
   <Button danger onClick={()=>remove(field.name)}>移除此字段</Button>
  </section>)}<Button onClick={()=>add({key:newCode("field"),type:"string",label:"",required:false,max_length:120})}>添加补充字段</Button></>}</Form.List>
  <h3>知识点与分类目录</h3><p>每行填写一个知识点或分类名称。不再使用的条目请关闭“允许新题目选择”，历史题目中的记录会保留。</p>
  <Form.List name="taxonomies">{(fields,{add,remove})=><>{fields.map(field=><section key={field.key}>
   <Form.Item name={[field.name,"id"]} hidden><Input /></Form.Item><Form.Item name={[field.name,"label"]} label="目录名称" rules={[{required:true}]}><Input /></Form.Item>
   <Form.List name={[field.name,"terms"]}>{(terms,ops)=><>{terms.map(term=><div key={term.key} className="question-bank-form-pair"><Form.Item name={[term.name,"id"]} hidden><Input /></Form.Item><Form.Item name={[term.name,"label"]} label="条目名称" rules={[{required:true}]}><Input placeholder="例如：函数的单调性" /></Form.Item><Form.Item name={[term.name,"active"]} label="允许新题目选择" valuePropName="checked"><Switch /></Form.Item></div>)}<Button onClick={()=>ops.add({id:newCode("term"),label:"",active:true})}>添加目录条目</Button></>}</Form.List>
   <Button danger onClick={()=>remove(field.name)}>移除此目录</Button>
  </section>)}<Button onClick={()=>add({id:newCode("catalog"),label:"",terms:[]})}>添加分类目录</Button>{!taxonomies.some((item:{id:string})=>item.id==="knowledge_points") ? <Button onClick={()=>add({id:"knowledge_points",label:"知识点",terms:[]})}>建立知识点目录</Button> : null}</>}</Form.List>
 </>;
}
