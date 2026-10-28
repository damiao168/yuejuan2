import { useEffect, type RefObject } from "react";
import { App, type FormInstance } from "antd";
import type { UseQueryResult } from "@tanstack/react-query";
import type { QuestionBank, QuestionBankACLResponse, QuestionBankMetadataSchemaResponse, UpdateQuestionBankMetadataSchemaRequest } from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { getUserErrorMessage } from "../../api/client";
import { metadataSchemaUpdate } from "./questionBankPageState";
import { isEditableACLRole, type ACLFormValues, type MetadataSchemaFormValues } from "./questionBankTypes";
import { bankRoleLabels, questionBankMemberAmbiguous, questionBankMemberLabel, type QuestionBankMember } from "./questionBankMembers";

type QueryState<T> = Pick<UseQueryResult<T, Error>, "data" | "isError" | "isLoading" | "isSuccess">;
type Command = <T>(operation: string, payload: unknown, execute: (headers: { "Idempotency-Key": string }) => Promise<T>) => Promise<T>;

interface AdministrationContext {
  bankId: string;
  bank?: QuestionBank;
  actorId: string;
  acl: QueryState<QuestionBankACLResponse>;
  currentSchema: QueryState<QuestionBankMetadataSchemaResponse>;
  members: QuestionBankMember[];
  aclOpen: boolean;
  schemaOpen: boolean;
  aclForm: FormInstance;
  schemaForm: FormInstance;
  schemaContext: RefObject<{ bankId: string; readable: boolean; formReady: boolean }>;
  saving: boolean;
  setSaving: (value: boolean) => void;
  setAclOpen: (value: boolean) => void;
  setSchemaOpen: (value: boolean) => void;
  setSchemaFormBankId: (value: string | null) => void;
  setSchemaReadFailureBankId: (value: string | null) => void;
  command: Command;
  refresh: () => Promise<void>;
}

/** 题库管理表单与写命令集中在这里；题目编辑与评分草稿仍由页面维护，不能因授权刷新重填题目内容。 */
export function useQuestionBankAdministration(context: AdministrationContext) {
  const { message, modal } = App.useApp();
  const { bankId, bank, actorId, acl, currentSchema, members, aclOpen, schemaOpen, aclForm, schemaForm,
    schemaContext, saving, setSaving, setAclOpen, setSchemaOpen, setSchemaFormBankId, setSchemaReadFailureBankId, command, refresh } = context;

  useEffect(() => {
    if (!aclOpen || !acl.data?.acl) return;
    aclForm.setFieldsValue({
      bindings: acl.data.acl.bindings
        .filter(binding => binding.preset !== "Custom" && binding.user_id !== bank?.created_by && binding.user_id !== actorId)
        .map(({ user_id, preset }) => ({ user_id, preset })),
      groups: acl.data.acl.groups.flatMap(group => isEditableACLRole(group.preset) ? [{
        id: group.id, name: group.name, preset: group.preset, members: group.member_user_ids,
      }] : []),
    });
  }, [acl.data, aclForm, aclOpen, bank?.created_by, actorId]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 题库身份变化必须清除旧元数据表单；读取失败不能沿用上一题库配置。
  useEffect(() => {
    if (schemaOpen) schemaForm.setFieldsValue({ fields: [], taxonomies: [] });
    setSchemaFormBankId(null);
  }, [bankId, schemaForm, schemaOpen, setSchemaFormBankId]);
  useEffect(() => {
    // 重试期间保留失败状态；只有成功读取当前题库规则后，才允许以空规则继续维护。
    if (currentSchema.isError) setSchemaReadFailureBankId(bankId);
    else if (currentSchema.isSuccess) setSchemaReadFailureBankId(null);
  }, [bankId, currentSchema.isError, currentSchema.isSuccess, setSchemaReadFailureBankId]);
  useEffect(() => {
    if (!schemaOpen || !currentSchema.isSuccess || !currentSchema.data?.schema) return;
    schemaForm.setFieldsValue({
      fields: currentSchema.data.schema.fields.map(field => ({
        ...field, options_text: field.options?.map(option => `${option.value}|${option.label}`).join("\n"),
      })),
      taxonomies: currentSchema.data.schema.taxonomies.map(taxonomy => ({
        terms: taxonomy.terms, id: taxonomy.id, label: taxonomy.label,
        terms_text: taxonomy.terms.map(term => `${term.id}|${term.label}`).join("\n"),
      })),
    });
    setSchemaFormBankId(bankId);
  }, [bankId, currentSchema.data, currentSchema.isSuccess, schemaForm, schemaOpen, setSchemaFormBankId]);

  async function saveACL(values: ACLFormValues) {
    if (!acl.data?.acl || acl.isError || acl.isLoading || saving) return;
    // 表单只提交预设职责；创建者、本人和历史组合权限由服务端在同一修订锁内保留。
    const retainedBindings = acl.data.acl.bindings.filter(binding => binding.preset === "Custom" || binding.user_id === bank?.created_by || binding.user_id === actorId);
    const retainedGroups = acl.data.acl.groups.filter(group => String(group.preset) === "Custom");
    const body = {
      expected_revision: acl.data.acl.revision,
      bindings: values.bindings ?? [],
      groups: (values.groups ?? []).map(({ members, ...group }) => ({
        ...group,
        member_user_ids: [...new Set((Array.isArray(members) ? members : (members ?? "").split(/\s+/)).map(id => id.trim()).filter(Boolean))],
      })),
    };
    if (body.bindings.some(binding => retainedBindings.some(old => old.user_id === binding.user_id)) || body.groups.some(group => retainedGroups.some(old => old.id === group.id || old.name.trim() === group.name.trim()))) {
      message.warning("只读保留的授权不能作为新职责覆盖，请选其他人员或使用不同组名。"); return;
    }
    const selectedIDs = new Set([...body.bindings.map(binding => binding.user_id), ...body.groups.flatMap(group => group.member_user_ids)]);
    if (members.some(person => selectedIDs.has(person.user_id) && questionBankMemberAmbiguous(person, members))) {
      message.warning("选中的同名人员缺少可区分的账号，请重新读取人员列表并核对身份后再更新授权。"); return;
    }
    const memberLabel = (id: string) => questionBankMemberLabel(members.find(person => person.user_id === id));
    // 确认摘要一次生成；行编号只用于显示，不进入授权请求或幂等指纹。
    const summary = body.bindings.map(binding => `${memberLabel(binding.user_id)}：${bankRoleLabels[binding.preset]}`)
      .concat(body.groups.map(group => `${group.name}：${group.member_user_ids.map(memberLabel).join("、") || "暂无人员"} · ${bankRoleLabels[group.preset]}`))
      .map(text => ({ key: crypto.randomUUID(), text }));
    modal.confirm({
      title: "确认更新题库授权？",
      content: <><p>将保存以下人员和分组的权限。管理结构与授权本身不包含浏览题目权限。</p><ul>{summary.map(line => <li key={line.key}>{line.text}</li>)}</ul><p>另有 {retainedBindings.length} 条用户授权、{retainedGroups.length} 个历史组合权限组原样保留。创建者、当前操作者和历史组合授权不会被本次表单删除或改写。</p></>,
      okText: "确认更新授权", cancelText: "返回核对", onOk: async () => {
        setSaving(true);
        try {
          await command(`acl:${bankId}`, body, headers => questionBankApi.updateQuestionBankACL({ path: { bankId }, body, headers }));
          setAclOpen(false);
          await refresh();
          message.success("题库权限已更新");
        } catch (error) {
          message.error(getUserErrorMessage(error, "权限更新失败"));
        } finally { setSaving(false); }
      },
    });
  }

  async function saveSchema(values: MetadataSchemaFormValues) {
    if (!bank || saving || schemaContext.current.bankId !== bankId || !schemaContext.current.formReady) {
      message.warning("请先成功读取当前题库的分类规则，再保存修改。"); return;
    }
    const body = metadataSchemaUpdate(values, bank.revision);
    const existingSchema = currentSchema.data!.schema;
    if (!body.fields.length && !body.taxonomies.length && (existingSchema.fields.length || existingSchema.taxonomies.length)) {
      // 清空规则会影响以后建题和检索；已有版本仍保留自己的规则，必须明确确认这一差别。
      modal.confirm({
        title: "清空当前题库的分类规则？",
        content: `将移除 ${existingSchema.fields.length} 个补充字段和 ${existingSchema.taxonomies.length} 个知识点或分类目录。新建题目将不再填写这些内容，组合检索也将没有当前知识点目录可选。已有题目版本继续保留原规则和记录，已有考试不受影响。`,
        okText: "确认清空并保存", okButtonProps: { danger: true }, cancelText: "返回编辑", onOk: () => persistSchema(body),
      });
      return;
    }
    await persistSchema(body);
  }

  async function persistSchema(body: UpdateQuestionBankMetadataSchemaRequest) {
    // 弹窗可能保留上一题库的回调；真正写入前再用页面的当前范围和读取状态核对，不能沿用旧表单。
    if (saving || schemaContext.current.bankId !== bankId || !schemaContext.current.formReady) {
      message.warning("当前题库或分类规则读取状态已变化，请重新核对后保存。"); return;
    }
    setSaving(true);
    try {
      await command(`schema:${bankId}`, body, headers => questionBankApi.updateQuestionBankMetadataSchema({ path: { bankId }, body, headers }));
      setSchemaOpen(false);
      await refresh();
      message.success("题目分类规则已更新，旧题目继续使用原规则");
    } catch (error) {
      message.error(getUserErrorMessage(error, "分类规则更新失败，请核对字段和目录条目"));
    } finally { setSaving(false); }
  }

  return { saveACL, saveSchema };
}
