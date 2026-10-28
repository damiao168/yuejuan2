import { useEffect, useRef, useState } from "react";
import { Alert, Button, Space } from "antd";
import { apiClient, getUserErrorMessage } from "../api/client";
import { getBusinessCommandSession } from "../api/businessCommand";
import { privateOpinionDraftPath, type PrivateOpinionDraftKind } from "../auth/reviewOpinionDraft";

interface Draft<T> { source: string; source_revision: number; values: T; updated_at: string; expires_at: string }
interface Response<T> {revision:number;source:string;source_revision:number;draft:Draft<T>|null}

/** 私有草稿只通过受原资源授权保护的接口保存；正文从不写入浏览器存储。 */
export function PrivateOpinionDraft<T extends object>({kind,recordId,source,sourceRevision,values,dirty,busy,onRestore}:Readonly<{
 kind:PrivateOpinionDraftKind;recordId:string;source:string;sourceRevision:number;values:T;dirty:boolean;busy:boolean;
 onRestore:(values:T,needsReview:boolean)=>void;
}>){
 const [revision,setRevision]=useState<number>(),[working,setWorking]=useState(false),[error,setError]=useState(""),[notice,setNotice]=useState("");
 const [reload,setReload]=useState(0),[loaded,setLoaded]=useState(false),[sessionChanged,setSessionChanged]=useState(false);
 const request=useRef(0),writing=useRef(false),live=useRef({values,dirty,onRestore});live.current={values,dirty,onRestore};
 const path=privateOpinionDraftPath(kind,recordId);
 useEffect(()=>{
  const invalidate=()=>{request.current++;writing.current=false;setSessionChanged(true);setLoaded(false);setRevision(undefined);setWorking(false);setNotice("");setError("登录状态已变化，请重新打开当前记录后处理草稿");};
  window.addEventListener("business-command-scope-changed",invalidate);return()=>window.removeEventListener("business-command-scope-changed",invalidate);
 },[]);
 // biome-ignore lint/correctness/useExhaustiveDependencies: reload是读取失败后的显式重试计数，不改变草稿资源或请求参数。
 useEffect(()=>{
  const generation=++request.current,session=getBusinessCommandSession();writing.current=false;setLoaded(false);setRevision(undefined);setWorking(false);setError("");setNotice("");
  if(sessionChanged){setError("登录状态已变化，请重新打开当前记录后处理草稿");return;}
  const current=()=>generation===request.current&&session===getBusinessCommandSession();
  void apiClient.request<Response<T>>(path).then(result=>{
   if(!current())return;
   if(result.source!==source||result.source_revision!==sourceRevision)throw new Error("记录来源已更新，请重新读取材料后再处理草稿");
   setRevision(result.revision);setLoaded(true);
   if(result.draft){live.current.onRestore(result.draft.values,live.current.dirty||result.draft.source_revision!==sourceRevision);setNotice("已读取本人私有草稿；核对后仍须正式提交。");}
  }).catch(reason=>{if(current())setError(getUserErrorMessage(reason,"私有草稿读取失败，当前输入仍只暂存在本次会话"));});
  return()=>{request.current++;};
 },[path,source,sourceRevision,reload,sessionChanged]);
 const save=async()=>{
  if(writing.current||busy||sessionChanged||revision===undefined||!loaded)return;
  const generation=request.current,session=getBusinessCommandSession(),snapshot=JSON.stringify(live.current.values);writing.current=true;setWorking(true);setError("");
  try{
   const result=await apiClient.request<Response<T>>(path,{method:"PUT",body:JSON.stringify({source,source_revision:sourceRevision,expected_revision:revision,values:JSON.parse(snapshot)})});
   if(generation!==request.current||session!==getBusinessCommandSession())return;
   setRevision(result.revision);setNotice(snapshot===JSON.stringify(live.current.values)?"已保存本人私有草稿，7天内可在重新登录后恢复。":"已保存上一版私有草稿；当前新增修改尚未保存。");
  }catch(reason){if(generation===request.current&&session===getBusinessCommandSession())setError(getUserErrorMessage(reason,"私有草稿未保存；当前输入已保留，请先读取云端草稿核对"));}
  finally{if(generation===request.current){writing.current=false;setWorking(false);}}
 };
 return <section aria-label="本人私有草稿"><Space wrap>
  <Button disabled={busy||working||!loaded||!dirty} loading={working} onClick={()=>void save()}>保存私有草稿</Button>
  {error?<Button disabled={busy||working} onClick={()=>setReload(value=>value+1)}>重新读取私有草稿</Button>:null}
 </Space>{error?<Alert type="warning" showIcon message={error}/>:null}{notice?<p role="status">{notice}</p>:null}</section>;
}
