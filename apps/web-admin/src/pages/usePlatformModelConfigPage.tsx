import { useQueryClient } from "@tanstack/react-query";
import { App as AntApp, Form } from "antd";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { autoCreateManagedModelAPIConfig, listAvailableManagedModels, listManagedModelAPIConfigs, updateManagedModelAPIConfig, type ManagedAPIProbeResult, type ManagedModelAPIConfig } from "../api/modelApiConfig";
import type { Tenant } from "../api/org";
import { usePageLoading } from "../components/pageReadiness";
import { onboardingQueryKey } from "../features/onboarding/queries";
import { modelConfigColumns } from "../features/platform-model-config/components/modelConfigColumns";
import { useChatModelAccess } from "../features/platform-model-config/hooks/useChatModelAccess";
import { useModelApplications } from "../features/platform-model-config/hooks/useModelApplications";
import { useModelConfigDrafts } from "../features/platform-model-config/hooks/useModelConfigDrafts";
import { useModelOperations } from "../features/platform-model-config/hooks/useModelOperations";
import { useSchoolModelActions, type ModelConfigPatch } from "../features/platform-model-config/hooks/useSchoolModelActions";
import { applySavedModelConfig, capabilityVerified, hasModelFormChanges, isSameModelAPIOrigin, loadAllSchoolTenants, modelConfigDraftKey, presetForConfig, supplierDefaults, textVerified, type ConfigFormValues, type SupplierPreset } from "../features/platform-model-config/lib/modelConfig";
import { selectModelSchool } from "../features/platform-model-config/lib/modelConfigLocation";
import { modelDiscoveryFailure, modelSaveFailure } from "../features/platform-model-config/lib/modelConfigNotices";
import { modelOperationKey } from "../features/platform-model-config/lib/modelOperationQueue";
import { routeQueryParam } from "../router/query";
import { useUnsavedChangesGuard } from "../router/useUnsavedChangesGuard";



export const disabledModelDiscoveryMessage = "此配置已停用，已保存的密钥暂不能用于获取模型列表。请关闭侧栏，在模型列表中启用后重试；或输入新 API Key 查询。";
// 状态、请求代次和互斥 ref 仍由同一次 Hook 调用拥有；视图只读取本次渲染的快照与动作。
export function usePlatformModelConfigPage() {
  const queryClient = useQueryClient();
  const { message, modal } = AntApp.useApp();
  const [form] = Form.useForm<ConfigFormValues>();
  const [schools, setSchools] = useState<Tenant[]>([]);
  const [schoolLoading, setSchoolLoading] = useState(true);
  const [selectedTenantID, setSelectedTenantID] = useState("");
  const chatAccess = useChatModelAccess(selectedTenantID);
  const [activeTab, setActiveTab] = useState<"models" | "bindings" | "governance">(() => {
    const requested = routeQueryParam("tab");
    return requested === "bindings" || requested === "governance" ? requested : "models";
  });
  const [bindingsDirty, setBindingsDirty] = useState(false);
  const [governanceDirty, setGovernanceDirty] = useState(false);
  const [governanceSaving, setGovernanceSaving] = useState(false);
  const governanceSavingRef = useRef(false);
  const handleGovernanceSaving = useCallback((busy: boolean) => { governanceSavingRef.current = busy; setGovernanceSaving(busy); }, []);
  const [modelFilter, setModelFilter] = useState<"all" | "enabled" | "connection" | "text" | "capability">("all");
  const [savedAccess, setSavedAccess] = useState<{ tenantID: string; config: ManagedModelAPIConfig }>();
  const [bindingsSaving, setBindingsSaving] = useState(false);
  const bindingsSavingRef = useRef(false);
  const handleBindingsSaving = useCallback((busy: boolean) => {
    bindingsSavingRef.current = busy;
    setBindingsSaving(busy);
  }, []);
  const [configs, setConfigs] = useState<ManagedModelAPIConfig[]>([]);
  const [configLoading, setConfigLoading] = useState(false);
  // 学校目录与选校后的模型配置分两次读取；正常的待选学校状态不额外阻塞切页。
  usePageLoading(schoolLoading || configLoading);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editing, setEditing] = useState<ManagedModelAPIConfig | null>(null);
  const [credentialSource, setCredentialSource] = useState<ManagedModelAPIConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [rotationState, setRotationState] = useState({ open: false, busy: false });
  const rotationBusyRef = useRef(false);
  const handleRotationState = useCallback((open: boolean, busy: boolean) => { rotationBusyRef.current = busy; setRotationState({ open, busy }); }, []);
  const { queue, operations } = useModelOperations();
  const applications = useModelApplications(selectedTenantID, queue);
  const [applicationTarget, setApplicationTarget] = useState<{ config: ManagedModelAPIConfig; type: "grading" | "document" | "answer_sheet" } | null>(null);
  const [applicationFormDirty, setApplicationDirty] = useState(false);
  const [applicationBindingsDirty, setApplicationBindingsDirty] = useState(false);
  // 独立用途保存只能清自己的编辑状态；可选角色绑定仍须单独保存或明确放弃。
  const applicationDirty = applicationFormDirty || applicationBindingsDirty;
  const [applicationSaving, setApplicationSaving] = useState(false);
  const applicationSavingRef = useRef(false);
  const handleApplicationSaving = useCallback((busy: boolean) => { applicationSavingRef.current = busy; setApplicationSaving(busy); }, []);
  const refreshApplications = applications.refresh;
  const closeApplication = () => {
    if (applicationSavingRef.current || queue.isBusy(selectedTenantID, "__document__") || queue.isBusy(selectedTenantID, "__answer_sheet__")) return;
    const close = () => {
      if (applicationSavingRef.current || queue.isBusy(selectedTenantID, "__document__") || queue.isBusy(selectedTenantID, "__answer_sheet__")) return;
      setApplicationTarget(null); setApplicationDirty(false); setApplicationBindingsDirty(false);
    };
    if (applicationDirty) modal.confirm({ title: "放弃未保存的用途设置？", content: "已保存的角色绑定和解析设置会保留。", okText: "放弃并关闭", cancelText: "继续编辑", onOk: close });
    else close();
  };
  const openApplication = useCallback((config: ManagedModelAPIConfig, type: "grading" | "document" | "answer_sheet") => {
    setApplicationDirty(false); setApplicationBindingsDirty(false); setApplicationTarget({ config, type });
  }, []);
  const mountedRef = useRef(true);
  const selectedTenantRef = useRef(selectedTenantID);
  selectedTenantRef.current = selectedTenantID;
  const configsRef = useRef(configs);
  configsRef.current = configs;
  const configPatchSequenceRef = useRef(0);
  const configPatchesRef = useRef(new Map<string, ModelConfigPatch>());
  const knownConfigVersionsRef = useRef(new Map<string, string>());
  const probeDetailsRef = useRef(new Map<string, { config: ManagedModelAPIConfig; result: ManagedAPIProbeResult }>());
  const [availableModels, setAvailableModels] = useState<string[]>([]);
  const [modelOptionsOpen, setModelOptionsOpen] = useState(false);
  const [filterAvailableModels, setFilterAvailableModels] = useState(false);
  const [modelsLoading, setModelsLoading] = useState(false);
  const configRequestRef = useRef(0);
  const discoveryRequestRef = useRef(0);
  const { clearFormDraft, saveFormDraft, restoreFormDraft } = useModelConfigDrafts(form);
  const watchedSupplier = Form.useWatch("supplier", form);
  const watchedAPIKey = Form.useWatch("api_key", form);
  const discoverySource = credentialSource ?? editing;
  const latestDiscoverySource = discoverySource ? configs.find((item) => item.id === discoverySource.id) ?? discoverySource : null;
  const modelDiscoveryDisabledReason = latestDiscoverySource?.status === "disabled" && (credentialSource || !String(watchedAPIKey ?? "").trim())
    ? disabledModelDiscoveryMessage : undefined;
  const invalidateModelDiscovery = useCallback(() => {
    discoveryRequestRef.current += 1;
    setModelsLoading(false);
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
  }, []);
  useEffect(() => {
    // 编辑期间刷新到停用状态时，作废旧查询和缓存；输入新密钥后可重新查询，配置仍保持停用。
    if (modelDiscoveryDisabledReason) invalidateModelDiscovery();
  }, [modelDiscoveryDisabledReason, invalidateModelDiscovery]);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; queue.cancelQueued(); };
  }, [queue]);

  const commitConfig = useCallback((tenantID: string, saved: ManagedModelAPIConfig | null, modelID: string, syncDefault = false) => {
    if (!mountedRef.current) return;
    const remember = (config: ManagedModelAPIConfig | null, id: string) => {
      knownConfigVersionsRef.current.set(modelOperationKey(tenantID, id), config?.updated_at ?? "deleted");
      configPatchesRef.current.set(modelOperationKey(tenantID, id), {
        sequence: ++configPatchSequenceRef.current, tenantID, modelID: id, config, syncDefault,
      });
    };
    remember(saved, modelID);
    if (tenantID !== selectedTenantRef.current) return;
    let next = configsRef.current;
    if (!saved) next = next.filter((item) => item.id !== modelID);
    else {
      if (syncDefault && saved.is_default) {
        for (const item of next) {
          if (item.id !== modelID && item.is_default) remember({ ...item, is_default: false }, item.id);
        }
        next = applySavedModelConfig(next, saved);
      } else next = next.map((item) => item.id === modelID ? saved : item);
      if (!next.some((item) => item.id === modelID)) next = [saved, ...next];
    }
    configsRef.current = next;
    setConfigs(next);
  }, []);
  useUnsavedChangesGuard({ dirty: (activeTab === "bindings" && bindingsDirty) || governanceDirty || applicationDirty || rotationState.open, busy: bindingsSaving || governanceSaving || saving || applicationSaving || applications.saving || rotationState.busy, description: "模型角色或学校使用策略尚未保存，离开后将重新读取服务端版本。" });
  // 三智能体角色配置只在点击各自保存按钮后写入服务端；离开当前学校或标签前先处理未保存输入。
  const changeBindingContext = (action: () => void) => {
    if (rotationBusyRef.current) return;
    // 子组件开始保存即同步通知；排队的学校切换和先前打开的确认不能在保存中卸载它。
    if (bindingsSavingRef.current || governanceSavingRef.current) return;
    const proceed = () => { if (!bindingsSavingRef.current && !governanceSavingRef.current) action(); };
    if ((activeTab !== "bindings" || !bindingsDirty) && !governanceDirty) { proceed(); return; }
    modal.confirm({
      title: "放弃未保存的模型设置？",
      content: "切换学校或标签后，当前输入不会保留。",
      okText: "放弃并继续", cancelText: "继续编辑", okButtonProps: { danger: true }, onOk: proceed
    });
  };
  // 切换学校立即递增请求代次，旧学校配置不得回填到当前模型列表。
  const selectedSchool = useMemo(
    () => schools.find((school) => school.id === selectedTenantID),
    [schools, selectedTenantID]
  );
  const currentConfig = useMemo(
    () => configs.find((config) => config.is_default && config.status === "active"),
    [configs]
  );

  const loadSchools = useCallback(async () => {
    setSchoolLoading(true);
    try {
      const items = await loadAllSchoolTenants();
      if (!mountedRef.current) return;
      setSchools(items);
      const requestedTenantID = routeQueryParam("tenant_id");
      setSelectedTenantID((current) => !current && !requestedTenantID && routeQueryParam("choose_school") === "1" ? "" : selectModelSchool(items, current, requestedTenantID));
      if (requestedTenantID && !items.some((school) => school.id === requestedTenantID)) {
        message.warning("地址中的学校已不可用，请重新选择配置学校。");
      }
    } catch {
      if (mountedRef.current) message.error("学校列表加载失败");
    } finally {
      if (mountedRef.current) setSchoolLoading(false);
    }
  }, [message]);

  const loadConfigs = useCallback(async (tenantID: string) => {
    const requestID = ++configRequestRef.current;
    const patchSequence = configPatchSequenceRef.current;
    if (!tenantID) {
      setConfigs([]);
      setConfigLoading(false);
      return;
    }
    setConfigLoading(true);
    try {
      const response = await listManagedModelAPIConfigs(tenantID);
      if (!mountedRef.current || requestID !== configRequestRef.current || tenantID !== selectedTenantRef.current) return;
      let next = response.configs;
      // 刷新期间可能已有检测或编辑完成；晚到的整表响应不能冲掉这些逐行结果。
      const patches = [...configPatchesRef.current.values()].filter((patch) => patch.tenantID === tenantID && patch.sequence > patchSequence)
        .sort((left, right) => left.sequence - right.sequence);
      for (const patch of patches) {
        if (!patch.config) next = next.filter((item) => item.id !== patch.modelID);
        else {
          const saved = patch.config;
          next = patch.syncDefault ? applySavedModelConfig(next, saved) : next.map((item) => item.id === saved.id ? saved : item);
          if (!next.some((item) => item.id === saved.id)) next = [saved, ...next];
        }
      }
      const ids = new Set(next.map((item) => item.id));
      for (const key of knownConfigVersionsRef.current.keys()) {
        if (key.startsWith(`${tenantID}:`) && !ids.has(key.slice(tenantID.length + 1))) knownConfigVersionsRef.current.set(key, "deleted");
      }
      for (const item of next) knownConfigVersionsRef.current.set(modelOperationKey(tenantID, item.id), item.updated_at);
      configsRef.current = next;
      setConfigs(next);
    } catch {
      if (!mountedRef.current || requestID !== configRequestRef.current || tenantID !== selectedTenantRef.current) return;
      message.error("模型 API 配置加载失败");
    } finally {
      if (mountedRef.current && requestID === configRequestRef.current) setConfigLoading(false);
    }
  }, [message]);

  useEffect(() => { void loadSchools(); }, [loadSchools]);
  useEffect(() => {
    setConfigs([]);
    void loadConfigs(selectedTenantID);
    return () => { configRequestRef.current += 1; };
  }, [loadConfigs, selectedTenantID]);

  const openCreate = () => {
    invalidateModelDiscovery();
    setEditing(null);
    setCredentialSource(null);
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    const restored = restoreFormDraft(modelConfigDraftKey(selectedTenantID), {
      supplier: undefined,
      provider_key: undefined,
      display_name: undefined,
      adapter_type: undefined,
      base_url: "",
      api_key: "",
      model_name: "",
      model_version: "",
      region: "global",
      enabled: true,
      is_default: configs.length === 0
    });
    setDrawerOpen(true);
    if (restored) message.info("已恢复 2 分钟内未保存的模型配置");
  };

  const openCreateWithKey = useCallback((config: ManagedModelAPIConfig) => {
    invalidateModelDiscovery();
    setEditing(null);
    setCredentialSource(config);
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    const restored = restoreFormDraft(modelConfigDraftKey(selectedTenantID, undefined, config.id), {
      supplier: presetForConfig(config),
      provider_key: config.provider_key,
      display_name: config.display_name,
      adapter_type: config.adapter_type,
      base_url: config.base_url,
      api_key: "",
      model_name: "",
      model_version: "",
      region: config.region,
      enabled: true,
      is_default: false
    });
    setDrawerOpen(true);
    if (restored) message.info("已恢复 2 分钟内未保存的模型配置");
  }, [invalidateModelDiscovery, message, restoreFormDraft, selectedTenantID]);

  const openEdit = useCallback((config: ManagedModelAPIConfig) => {
    invalidateModelDiscovery();
    setEditing(config);
    setCredentialSource(null);
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    const restored = restoreFormDraft(modelConfigDraftKey(selectedTenantID, config.id), {
      supplier: presetForConfig(config),
      provider_key: config.provider_key,
      display_name: config.display_name,
      adapter_type: config.adapter_type,
      base_url: config.base_url,
      api_key: "",
      model_name: config.model_name,
      model_version: config.model_version,
      region: config.region,
      enabled: config.status === "active",
      is_default: config.is_default
    });
    setDrawerOpen(true);
    if (restored) message.info("已恢复 2 分钟内未保存的编辑内容");
  }, [invalidateModelDiscovery, message, restoreFormDraft, selectedTenantID]);

  const changeSupplier = (supplier: SupplierPreset) => {
    if (editing || credentialSource) return;
    invalidateModelDiscovery();
    form.setFieldsValue({ ...supplierDefaults[supplier], supplier, base_url: "", model_name: "" });
    setAvailableModels([]);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
  };

  const fetchAvailableModels = async () => {
    if (!selectedTenantID || modelsLoading) return;
    const tenantID = selectedTenantID;
    const requestID = ++discoveryRequestRef.current;
    // 侧栏关闭、换模型或更换连接输入后，旧密钥查询得到的模型列表不能再填入新表单。
    const current = () => mountedRef.current && requestID === discoveryRequestRef.current && tenantID === selectedTenantRef.current;
    let values: ConfigFormValues;
    try {
      const supplier = form.getFieldValue("supplier") as SupplierPreset | undefined;
      const apiKey = String(form.getFieldValue("api_key") ?? "").trim();
      const canReuseSavedCredential = Boolean(credentialSource) || Boolean(editing && !apiKey && (
        supplier !== "custom" || isSameModelAPIOrigin(editing.base_url, String(form.getFieldValue("base_url") ?? ""))
      ));
      await form.validateFields([
        "supplier",
        ...(supplier === "custom" ? ["base_url"] : []),
        ...(!canReuseSavedCredential ? ["api_key"] : [])
      ]);
      values = form.getFieldsValue();
    } catch {
      return;
    }
    if (!current()) return;
    const savedCredential = credentialSource ?? (editing && !String(values.api_key ?? "").trim() ? editing : null);
    if (savedCredential && (configsRef.current.find((item) => item.id === savedCredential.id) ?? savedCredential).status !== "active") {
      message.warning(disabledModelDiscoveryMessage);
      return;
    }
    setModelsLoading(true);
    try {
      const apiKey = String(values.api_key ?? "").trim();
      const reusableCredential = credentialSource ?? (editing && !apiKey ? editing : null);
      const response = await listAvailableManagedModels({
        tenant_id: tenantID,
        ...(reusableCredential ? { credential_source_id: reusableCredential.id } : { api_key: apiKey }),
        provider: values.supplier,
        base_url: values.supplier === "custom" ? values.base_url?.trim() : undefined
      });
      if (!current()) return;
      setAvailableModels(response.models);
      setFilterAvailableModels(false);
      setModelOptionsOpen(response.models.length > 0);
      if (response.models.length > 0) {
        message.success(`已从${response.provider.display_name}获取 ${response.models.length} 个模型，请选择`);
      } else {
        message.warning(`${response.provider.display_name}没有返回可用模型`);
      }
    } catch (error) {
      if (!current()) return;
      setModelOptionsOpen(false);
      setFilterAvailableModels(false);
      const notice = modelDiscoveryFailure(error, credentialSource !== null || Boolean(editing));
      const notify = message[notice.kind];
      notify(notice.text);
    } finally {
      if (current()) setModelsLoading(false);
    }
  };

  const save = async () => {
    if (!selectedTenantID || saving) return;
    const tenantID = selectedTenantID;
    const release = queue.acquireWrite(tenantID, editing?.id ?? "__create__");
    if (!release) return;
    setSaving(true);
    let values: ConfigFormValues;
    try {
      values = await form.validateFields();
    } catch {
      release();
      if (mountedRef.current) setSaving(false);
      return;
    }
    if (!mountedRef.current) { release(); return; }
    // 编辑只修改表单中的连接字段；侧栏打开期间别处完成的停用或默认切换不能被旧快照改回去。
    const currentEditing = editing ? configsRef.current.find((item) => item.id === editing.id) : null;
    if (editing && !currentEditing) { release(); setSaving(false); message.warning("该模型已发生变化，请关闭侧栏后重新打开"); return; }
    const releaseDefault = currentEditing?.is_default ? queue.acquireWrite(tenantID, "__default__") : undefined;
    if (currentEditing?.is_default && !releaseDefault) { release(); setSaving(false); return; }
    const draftKey = modelConfigDraftKey(selectedTenantID, editing?.id, credentialSource?.id);
    const apiKey = String(values.api_key ?? "").trim();
    try {
      if (editing) {
        const response = await updateManagedModelAPIConfig(editing.id, selectedTenantID, {
          // 使用打开表单时的版本，不能用刷新后的列表版本替旧表单背书。
          expected_updated_at: editing.updated_at,
          display_name: editing.display_name,
          adapter_type: editing.adapter_type,
          base_url: values.supplier === "custom" ? String(values.base_url ?? "").trim() : editing.base_url,
          api_key: apiKey,
          model_name: values.model_name.trim(),
          model_version: values.model_name.trim(),
          region: editing.region,
          status: currentEditing!.status,
          is_default: currentEditing!.is_default
        });
        commitConfig(tenantID, response.config, response.config.id, true);
        probeDetailsRef.current.delete(modelOperationKey(tenantID, response.config.id));
        if (!mountedRef.current) return;
        message.success(apiKey ? "配置和密钥已更新" : "配置已更新");
      } else {
        const response = await autoCreateManagedModelAPIConfig({
          tenant_id: selectedTenantID,
          ...(credentialSource ? { credential_source_id: credentialSource.id } : { api_key: apiKey }),
          model_name: values.model_name.trim(),
          provider: values.supplier,
          base_url: values.supplier === "custom" ? values.base_url?.trim() : undefined
        });
        commitConfig(tenantID, response.config, response.config.id, true);
        if (!mountedRef.current) return;
        setSavedAccess({ tenantID: selectedTenantID, config: response.config });
        message.success(`${response.config.display_name} 已保存；密钥及模型访问权限检查通过，尚未验证回复能力。`);
      }
      clearFormDraft(draftKey);
      invalidateModelDiscovery();
      setDrawerOpen(false);
      setModelOptionsOpen(false);
      form.resetFields();
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
    } catch (error) {
      const notice = modelSaveFailure(error, editing);
      const notify = message[notice.kind];
      if (mountedRef.current) notify(notice.text);
    } finally {
      releaseDefault?.();
      release();
      if (mountedRef.current) setSaving(false);
    }
  };

  const closeDrawer = () => {
    if (saving) return;
    invalidateModelDiscovery();
    const values = form.getFieldsValue(true) as ConfigFormValues;
    const hasChanges = hasModelFormChanges(values, editing, credentialSource);
    const draftKey = modelConfigDraftKey(selectedTenantID, editing?.id, credentialSource?.id);
    if (hasChanges) {
      saveFormDraft(draftKey, values);
      message.info("未保存内容已暂存 2 分钟");
    } else {
      clearFormDraft(draftKey);
    }
    setDrawerOpen(false);
    setModelOptionsOpen(false);
    setFilterAvailableModels(false);
    form.resetFields();
  };

  const { showConnectionDetails, showCapabilityDetails, showTextDetails, probe, probeAll, stopBatch, updateUsage, removeConfig, batchProbeProgress } = useSchoolModelActions({
    selectedTenantID, configs, configLoading, queue, mountedRef, selectedTenantRef, configPatchSequenceRef,
    configPatchesRef, knownConfigVersionsRef, probeDetailsRef, commitConfig, loadConfigs
  });

  const credentialHintIDs = useMemo(() => {
    // 同名模型才补充脱敏尾号；配置身份和请求路由始终使用原有 id，尾号不保证唯一。
    const counts = new Map<string, number>();
    const keyFor = (config: ManagedModelAPIConfig) => JSON.stringify([config.provider_key, config.model_name]);
    for (const config of configs) counts.set(keyFor(config), (counts.get(keyFor(config)) ?? 0) + 1);
    return new Set(configs.filter((config) => (counts.get(keyFor(config)) ?? 0) > 1).map((config) => config.id));
  }, [configs]);
  const gradingModelIDs = useMemo(() => new Set(applications.data?.grading_model_ids ?? []), [applications.data]);
  const documentFollowsChat = applications.data?.document_selection.mode === "follow_chat";
  const documentModelID = documentFollowsChat ? currentConfig?.id : applications.data?.document_selection.model_config_id;
  const columns = useMemo(() => modelConfigColumns({
    chatModelIDs: chatAccess.ids, chatAccessBusy: chatAccess.busy, toggleChatAccess: chatAccess.toggle,
    operations, tenantID: selectedTenantID, credentialHintIDs, openEdit, probe,
    applicationsReady: Boolean(applications.data) && !applications.loading && !applications.error,
    gradingModelIDs, documentModelID, documentFollowsChat, openApplication,
    answerSheetReady: Boolean(applications.data?.answer_sheet_selection),
    answerSheetModelID: applications.data?.answer_sheet_selection?.mode === "model" ? applications.data.answer_sheet_selection.model_config_id : undefined,
    removeConfig, showConnectionDetails, showTextDetails, showCapabilityDetails, updateUsage,
  }), [chatAccess.ids, chatAccess.busy, chatAccess.toggle, operations, selectedTenantID, credentialHintIDs, applications.data, applications.loading, applications.error, gradingModelIDs, documentModelID, documentFollowsChat, openApplication, openEdit, probe, removeConfig, showConnectionDetails, showTextDetails, showCapabilityDetails, updateUsage]);

  const activeCount = configs.filter((config) => config.status === "active").length;
  const healthyCount = configs.filter((config) => config.status === "active" && config.last_test_status === "success").length;
  const textCount = configs.filter((config) => config.status === "active" && textVerified(config)).length;
  const capabilityCount = configs.filter((config) => config.status === "active" && capabilityVerified(config)).length;
  const visibleConfigs = configs.filter((config) => modelFilter === "all" || (config.status === "active" && (modelFilter === "enabled" || (modelFilter === "connection" ? config.last_test_status === "success" : modelFilter === "text" ? textVerified(config) : capabilityVerified(config)))));
  // 开放范围与检测资格分别统计，筛选表格不能改变学校实际可选的配置数量。
  const openedChatConfigs = configs.filter(config => chatAccess.ids?.includes(config.id));
  const availableChatCount = openedChatConfigs.filter(config => config.status === "active" && config.credential_configured && textVerified(config)).length;
  const temporarilyUnavailableCount = configs.filter((config) => config.last_test_status === "temporary_unavailable").length;
  const editingBusy = Boolean(editing && (queue.isBusy(selectedTenantID, editing.id) ||
    configs.find((item) => item.id === editing.id)?.is_default && queue.isBusy(selectedTenantID, "__default__")));
  const latestEditingConfig = editing ? configs.find((item) => item.id === editing.id) ?? editing : null;
  return {
    message, form, schools, schoolLoading,
    selectedTenantID, setSelectedTenantID, chatAccess, activeTab,
    setActiveTab, setBindingsDirty, setGovernanceDirty, governanceSaving,
    handleGovernanceSaving, modelFilter, setModelFilter, savedAccess,
    setSavedAccess, bindingsSaving, bindingsSavingRef, handleBindingsSaving,
    configs, setConfigs, configLoading, setConfigLoading,
    drawerOpen, editing, credentialSource, saving,
    rotationState, handleRotationState, queue, applications,
    applicationTarget, setApplicationTarget, setApplicationDirty, setApplicationBindingsDirty,
    applicationSaving, handleApplicationSaving, refreshApplications, closeApplication,
    selectedTenantRef, configsRef, probeDetailsRef, availableModels,
    setAvailableModels, modelOptionsOpen, setModelOptionsOpen, filterAvailableModels,
    setFilterAvailableModels, modelsLoading, configRequestRef, watchedSupplier,
    modelDiscoveryDisabledReason, invalidateModelDiscovery, commitConfig, changeBindingContext,
    selectedSchool, currentConfig, loadSchools, loadConfigs,
    openCreate, openCreateWithKey, changeSupplier, fetchAvailableModels,
    save, closeDrawer, probe, probeAll,
    stopBatch, updateUsage, batchProbeProgress, columns,
    activeCount, healthyCount, textCount, capabilityCount,
    visibleConfigs, openedChatConfigs, availableChatCount, temporarilyUnavailableCount,
    editingBusy, latestEditingConfig,
  };
}
