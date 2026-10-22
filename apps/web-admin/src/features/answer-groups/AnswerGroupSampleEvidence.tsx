import { useEffect, useRef, useState } from "react";
import { Alert, Button } from "antd";
import { getAnswerGroupMemberContext, getAnswerGroupMemberImage, type AnswerGroupMemberContext } from "../../api/answerGroups";
import { getUserErrorMessage } from "../../api/client";
import { TaskScoringReference } from "../../components/TaskScoringReference";
import { MathMarkdown } from "../../components/MathMarkdown";

function renderSampleMaterial(value: AnswerGroupMemberContext | undefined, error: string) {
 if (value) {
  return <><TaskScoringReference question={value.question} rubric={value.question.rubric}/><h4>本样本答题内容</h4><MathMarkdown>{value.answer_text || "未提供答题文字，请等待原图显示后复核"}</MathMarkdown></>;
 }
 if (error) {
  return null;
 }
 // 这里只提示请求仍在等待，不表示材料已读或操作完成。
 return <p><output>正在读取本样本…</output></p>;
}

export function AnswerGroupSampleEvidence({ groupId, segmentId, hash, onRead }: Readonly<{ groupId: string; segmentId: string; hash: string; onRead: (value: AnswerGroupMemberContext) => void }>) {
 const [open,setOpen]=useState(false);const [attempt,setAttempt]=useState(0);const [value,setValue]=useState<AnswerGroupMemberContext>();const [error,setError]=useState("");const [url,setUrl]=useState("");const [imageError,setImageError]=useState("");
 // 父组件刷新可能更换回调；请求完成时使用最新登记入口，不因此重新读取材料。
 const read = useRef(onRead); read.current = onRead;
 // biome-ignore lint/correctness/useExhaustiveDependencies: attempt 由重试样本按钮递增；同组同片段也要重新核对文字与图片。
 useEffect(()=>{
  if(!open) {
   return;
  }
  const controller=new AbortController();let objectUrl="";setValue(undefined);setError("");setUrl("");setImageError("");
  getAnswerGroupMemberContext(groupId,segmentId,controller.signal).then(response=>{
   if(controller.signal.aborted) {
    return;
   }
   const context=response.member_context;
   if(context.group_id!==groupId||context.member.segment_id!==segmentId||context.member.representation_hash!==hash) {
    throw new Error("样本已变化，请重新生成分组后复核");
   }
   setValue(context);
   // 非空文字可直接登记已读；没有文字时仍读取原图，由图片成功显示后登记。
   if(context.answer_text.trim()) {
    read.current(context);
   }
   return getAnswerGroupMemberImage(groupId,segmentId,controller.signal).then(file=>{
    if(controller.signal.aborted) {
     return;
    }
    objectUrl=URL.createObjectURL(file.blob);setUrl(objectUrl);
   }).catch(cause=>{if(!controller.signal.aborted)setImageError(getUserErrorMessage(cause,"样本答题图读取失败"));});
  }).catch(cause=>{if(!controller.signal.aborted)setError(getUserErrorMessage(cause,"样本材料读取失败"));});
  return()=>{controller.abort();if(objectUrl)URL.revokeObjectURL(objectUrl);};
 // 登记材料只认可本样本的实际文字或成功显示的图片；组/样本/hash变化必须重新读取。
 },[groupId,segmentId,hash,open,attempt]);
 if(!open)return <Button size="small" onClick={()=>setOpen(true)}>查看实际答题样本</Button>;
 return <section aria-label="分组实际答题样本">
  {error?<Alert type="error" showIcon message={error} action={<Button onClick={()=>setAttempt(x=>x+1)}>重试样本材料</Button>}/>:null}
  {renderSampleMaterial(value, error)}
  {url?<img src={url} alt="分组样本答题图" style={{maxWidth:"100%",maxHeight:340}} onLoad={()=>{if(value)read.current(value);}} onError={()=>{setUrl("");setImageError("样本答题图显示失败");}}/>:null}
  {imageError?<Alert type="warning" message={imageError} action={<Button onClick={()=>setAttempt(x=>x+1)}>重试样本答题图</Button>}/>:null}
 </section>;
}
