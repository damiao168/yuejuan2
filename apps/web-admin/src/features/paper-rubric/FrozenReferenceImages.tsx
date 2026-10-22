import { Alert, Button } from "antd";
import { useEffect, useState } from "react";
import { getTaskReferenceImage, getTaskReferenceMaterials, getRunReferenceImage, getRunReferenceMaterials, type ReferenceMaterials } from "../../api/referenceMaterials";
import { getBusinessCommandSession } from "../../api/businessCommand";
import { ProtectedReferenceImage, referenceRoleLabel, useReferenceSession } from "./ReferenceMaterialEditor";

export function FrozenReferenceImages({ taskId, runId, questionId }: Readonly<{ taskId?: string; runId?: string; questionId?: string }>) {
  const session=useReferenceSession();const identity=`${session.scope}:${session.generation}:${taskId}:${runId}:${questionId}`;
  return <FrozenReferenceImagesContent key={identity} identity={identity} taskId={taskId} runId={runId} questionId={questionId}/>;
}
function FrozenReferenceImagesContent({ taskId, runId, questionId,identity }: Readonly<{ taskId?: string; runId?: string; questionId?: string;identity:string }>) {
 const [materials,setMaterials]=useState<ReferenceMaterials|null>(null);const [error,setError]=useState(false);const [reload,setReload]=useState(0);
 const session=useReferenceSession();
 useEffect(()=>{let disposed=false;setError(false);setMaterials(null);
   const load=taskId?getTaskReferenceMaterials(taskId):runId&&questionId?getRunReferenceMaterials(runId,questionId):null;
   if(load)void load.then(result=>{if(!disposed&&getBusinessCommandSession()===session)setMaterials(result.materials);}).catch(()=>{if(!disposed&&getBusinessCommandSession()===session)setError(true);});
   return()=>{disposed=true;};
 },[taskId,runId,questionId,session,reload]);
 if(error)return <Alert type="warning" showIcon message="冻结参考图读取失败，请人工核对材料" action={<Button onClick={()=>setReload(value=>value+1)}>重新读取</Button>}/>;
 if(!materials)return <p>正在核对冻结参考图…</p>;
 if(materials.mode==="legacy_unrecorded")return <p className="muted">历史材料模式未记录，不能据此推断图片曾发送给模型。</p>;
 if(materials.mode==="text_only")return <p>本题已明确仅使用文字参考。</p>;
 if(materials.use_state!=="active")return <Alert type="warning" message="本题材料使用授权已撤回，后续 AI 建议已阻断"/>;
 return <section aria-label="本次冻结参考图"><p>第 {materials.revision} 版，共 {materials.items.length} 张；此处用于核对本题评分依据。</p>{materials.items.map(item=><figure key={item.id}>
 <figcaption>{referenceRoleLabel(item.role)} {item.ordinal}</figcaption><ProtectedReferenceImage identity={`${identity}:${item.id}`} alt={`${referenceRoleLabel(item.role)} ${item.ordinal}`} load={()=>taskId?getTaskReferenceImage(taskId,item.id):getRunReferenceImage(runId!,questionId!,item.id)}/>
 </figure>)}</section>;
}
