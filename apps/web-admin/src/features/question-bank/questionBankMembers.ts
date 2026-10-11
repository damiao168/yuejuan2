import { questionBankApi } from "../../api/questionBank";
import type { QuestionBankMember } from "@edugrade/sdk";

export type { QuestionBankMember };
export async function listQuestionBankMembers(bankId: string) {
 return questionBankApi.listQuestionBankMembers({path:{bankId}});
}

export function questionBankMemberLabel(member?: QuestionBankMember) {
 const name = member?.display_name.trim() || "已有授权成员（姓名暂不可读取）";
 return member?.username?.trim() ? `${name}（账号：${member.username.trim()}）` : name;
}

export function questionBankMemberAmbiguous(member: QuestionBankMember, members: QuestionBankMember[]) {
 // 旧接口或未取得账号时不能让管理者按同名选项猜人；已有授权仍按原编号保留。
 return members.filter(other => questionBankMemberLabel(other) === questionBankMemberLabel(member)).length > 1;
}

export const bankRoleLabels = {Viewer:"浏览题目",Author:"出题与编辑",Reviewer:"审核题目",Publisher:"发布题目",Manager:"管理结构与授权"};
export const bankRoleOptions = Object.entries(bankRoleLabels).map(([value,label])=>({value,label}));
