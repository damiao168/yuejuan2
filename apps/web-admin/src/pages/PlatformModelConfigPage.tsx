import { Alert, Button, Segmented, Space, Tooltip } from "antd";
import { Plus, RefreshCw, Zap } from "lucide-react";
import { ManagedGovernanceWorkspace } from "../components/model-governance/ManagedGovernanceWorkspace";
import { AnswerSheetModelForm } from "../features/platform-model-config/components/AnswerSheetModelForm";
import { CredentialRotation } from "../features/platform-model-config/components/CredentialRotation";
import { DocumentModelForm } from "../features/platform-model-config/components/DocumentModelForm";
import { ImageGradingModelForm } from "../features/platform-model-config/components/ImageGradingModelForm";
import { ModelConfigDrawer } from "../features/platform-model-config/components/ModelConfigDrawer";
import { ModelConfigForm } from "../features/platform-model-config/components/ModelConfigForm";
import { SchoolModelSelector } from "../features/platform-model-config/components/SchoolModelSelector";
import { SchoolModelTable } from "../features/platform-model-config/components/SchoolModelTable";
import { modelSaveLabel } from "../features/platform-model-config/lib/modelConfig";
import { rememberModelConfigLocation } from "../features/platform-model-config/lib/modelConfigLocation";
import { savedModelAccessDescription } from "../features/platform-model-config/lib/modelConfigNotices";
import { modelOperationKey } from "../features/platform-model-config/lib/modelOperationQueue";
import { replaceRouteQuery } from "../router/query";
import { PanelModelBindingsSection } from "./PanelModelBindingsSection";
import { usePlatformModelConfigPage } from "./usePlatformModelConfigPage";
export function PlatformModelConfigPage() {
  const {
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
  } = usePlatformModelConfigPage();


  return (
    <div className="platform-model-page">
      {chatAccess.error ? <Alert type="error" showIcon message={chatAccess.error} action={<Button size="small" disabled={chatAccess.busy} onClick={() => void chatAccess.refresh()}>重新读取开放设置</Button>} /> : null}
      <section className="page-heading platform-model-heading">
        <div>
          <h1>模型管理</h1>
          <p>选择学校，管理学校模型、三智能体评分和模型治理。</p>
        </div>
        <Space>
          <Button icon={<RefreshCw size={16} />} disabled={bindingsSaving || saving} loading={schoolLoading || configLoading} onClick={() => {
            if (bindingsSavingRef.current) return;
            void loadSchools();
            void loadConfigs(selectedTenantID);
            void refreshApplications();
            void chatAccess.refresh();
          }}>刷新</Button>
          {activeTab === "models" ? <Button type="primary" icon={<Plus size={16} />} disabled={!selectedTenantID || saving} onClick={openCreate}>添加模型</Button> : null}
        </Space>
      </section>

      <SchoolModelSelector
        schools={schools}
        selectedTenantID={selectedTenantID}
        currentConfig={currentConfig}
        loading={schoolLoading}
        disabled={drawerOpen || Boolean(applicationTarget) || bindingsSaving || governanceSaving || saving || applications.saving || rotationState.open}
        activeCount={activeCount}
        healthyCount={healthyCount}
        textCount={textCount}
        capabilityCount={capabilityCount}
        chatModelCount={chatAccess.ids && !configLoading ? openedChatConfigs.length : undefined}
        availableChatModelCount={chatAccess.ids && !configLoading ? availableChatCount : undefined}
        localSwitchBusy={queue.isBusy(selectedTenantID, "__default__") || Boolean(currentConfig && queue.isBusy(selectedTenantID, currentConfig.id))}
        onUseLocal={() => { if (currentConfig) void updateUsage(currentConfig, "active", false); }}
        onFilter={(filter) => changeBindingContext(() => { setModelFilter(filter); setActiveTab("models"); rememberModelConfigLocation(selectedTenantID, "models"); })}
        temporarilyUnavailableCount={temporarilyUnavailableCount}
        onChange={(tenantID) => changeBindingContext(() => {
          configRequestRef.current += 1;
          selectedTenantRef.current = tenantID;
          configsRef.current = [];
          setSavedAccess(undefined); setModelFilter("all");
          setConfigs([]);
          setConfigLoading(true);
          rememberModelConfigLocation(tenantID, activeTab);
          setSelectedTenantID(tenantID);
        })}
      />

      <Alert
        className="platform-model-security-note"
        type="info"
        showIcon
        message="按用途独立配置学校模型；连接通过不代表已通过各用途检测。"
        description={<details className="platform-model-usage-guide"><summary>用途与检测说明</summary>
          <p><strong>日常对话：</strong>普通回复检测通过后，可勾选多个供应商的模型。学校管理员在聊天输入框内切换。</p>
          <p><strong>试卷解析：</strong>通过结构化输出检测后，可独立指定模型或跟随默认配置；学校管理员切换聊天模型不会改变试卷解析。</p>
          <p><strong>答题卡识别：</strong>在“应用用途”中独立分配多模态模型，直接分析答题卡图像；识别结果需人工核对后发布。</p>
          <p><strong>自动阅卷与评分：</strong>点击“阅卷配置”指定本校自动阅卷 API 模型并检测图片能力，系统不会回退本地模型。多模型评审角色可按需展开；模型准入与最终确认继续按已配置策略执行。</p>
          <small>连接检查只验证接口、密钥和模型访问权限。同一学校可为同一供应商和模型配置多个 API Key，密钥分别加密保存。</small>
        </details>}
      />

      <Segmented
        value={activeTab}
        disabled={bindingsSaving || rotationState.open}
        options={[{ value: "models", label: "学校模型" }, { value: "bindings", label: "三智能体配置" }, { value: "governance", label: "治理与评测" }]}
        onChange={(value) => {
          const next = value as "models" | "bindings" | "governance";
          changeBindingContext(() => {
            setActiveTab(next);
            rememberModelConfigLocation(selectedTenantID, next);
          });
        }}
      />

      {activeTab === "models" ? <><div className="platform-model-library-heading">
        <h2 className="platform-model-section-title">学校模型库</h2>
        <Space><CredentialRotation tenantID={selectedTenantID} configs={configs} disabled={saving || configLoading || drawerOpen || Boolean(applicationTarget)} queue={queue} onConfigSaved={(owner, config) => { commitConfig(owner, config, config.id, true); probeDetailsRef.current.delete(modelOperationKey(owner, config.id)); }} onStateChange={handleRotationState} /><Tooltip title="检测当前学校所有厂商的模型配置（含已停用配置），只检查连接，不发送模型生成请求">
          <Button icon={<Zap size={16} />} loading={Boolean(batchProbeProgress)}
            disabled={!selectedTenantID || configLoading || configs.length === 0 || Boolean(batchProbeProgress)}
            onClick={() => void probeAll()}>
            {batchProbeProgress ? `检测中 ${batchProbeProgress.completed}/${batchProbeProgress.total}` : "检测全部连接"}
          </Button>
        </Tooltip>{batchProbeProgress ? <Button disabled={batchProbeProgress.stopped} onClick={stopBatch}>{batchProbeProgress.stopped ? "等待已发出的检测结束" : "停止后续检测"}</Button> : null}</Space>
      </div>
      {savedAccess?.tenantID === selectedTenantID ? <Alert type="success" showIcon message={`${savedAccess.config.display_name}：密钥及模型访问权限检查通过`} description={savedModelAccessDescription(configs.find((item) => item.id === savedAccess.config.id))} action={<Space><Button disabled={queue.isBusy(selectedTenantID, savedAccess.config.id)} onClick={() => void probe(savedAccess.config, "text")}>检测普通回复</Button><Button disabled={queue.isBusy(selectedTenantID, savedAccess.config.id)} onClick={() => void probe(savedAccess.config, "structured")}>检测结构化输出</Button></Space>} /> : null}
      <Space><span>{({ all: "全部配置", enabled: "已启用配置", connection: "已启用且访问检查通过", text: "已启用且普通回复已验证", capability: "已启用且结构化能力已验证" })[modelFilter]} · {visibleConfigs.length} 个</span>{modelFilter !== "all" ? <Button onClick={() => setModelFilter("all")}>显示全部</Button> : null}</Space>
      <SchoolModelTable tenantID={selectedTenantID} loading={configLoading && configs.length === 0} columns={columns} configs={visibleConfigs} />
      {applications.error && !applicationTarget ? <Alert type="warning" showIcon message={`用途配置暂不可用：${applications.error}`} action={<Button loading={applications.loading} onClick={() => void refreshApplications()}>刷新用途</Button>} /> : null}
      </> : null}
      {activeTab === "bindings" ? <PanelModelBindingsSection tenantID={selectedTenantID} configs={configs} loadingConfigs={configLoading} onSaved={() => void refreshApplications()} onDirtyChange={setBindingsDirty} onSavingChange={handleBindingsSaving} onOpenGovernance={(view, scope) => changeBindingContext(() => {
        rememberModelConfigLocation(selectedTenantID, "governance");
        replaceRouteQuery((params) => {
          params.set("view", view);
          // 评分组合入口没有题目风险事实；旧地址中的风险不能沿用为新组合的检查范围。
          params.delete("risk");
          if (scope) { params.set("stage", scope.stage); params.set("subject", scope.subject); params.set("archetype", scope.archetype); }
        });
        setActiveTab("governance");
      })} /> : null}
      {activeTab === "governance" ? <ManagedGovernanceWorkspace key={selectedTenantID} tenantID={selectedTenantID} schoolName={selectedSchool?.name} configs={configs} onDirtyChange={setGovernanceDirty} onSavingChange={handleGovernanceSaving} /> : null}

      <ModelConfigDrawer title={applicationTarget ? `${applicationTarget.config.model_name}：${applicationTarget.type === "grading" ? "阅卷配置" : applicationTarget.type === "answer_sheet" ? "答题卡识别" : "试卷解析"}` : "应用用途"}
        width={applicationTarget?.type === "grading" ? 1120 : 560} open={Boolean(applicationTarget)} destroyOnHidden
        closable={!applicationSaving && !applications.saving} maskClosable={!applicationSaving && !applications.saving} keyboard={!applicationSaving && !applications.saving} onClose={closeApplication}>
        {applicationTarget?.type === "grading" ? <><ImageGradingModelForm tenantID={selectedTenantID} config={configs.find(config => config.id === applicationTarget.config.id) ?? applicationTarget.config} selection={applications.data?.grading_selection} loading={applications.loading} busy={applications.saving} error={applications.error} onRefresh={() => void refreshApplications()} onDirtyChange={setApplicationDirty} onSave={mode => applications.saveGrading(mode, applicationTarget.config.id).then(saved => { if (saved) { setApplicationDirty(false); message.success(mode === "model" ? "自动阅卷 API 模型已保存，正在验证图片能力" : "已取消自动阅卷用途"); void refreshApplications(); } return saved; })} /><details><summary>多模型评审角色（可选；单模型可直接测试）</summary><PanelModelBindingsSection key={`${selectedTenantID}:${applicationTarget.config.id}`} tenantID={selectedTenantID} configs={configs} loadingConfigs={configLoading}
          suggestedModelID={applicationTarget.config.id} savedUses={applications.data?.grading_uses?.filter(use => use.model_config_id === applicationTarget.config.id)} onSaved={() => void refreshApplications()} onDirtyChange={setApplicationBindingsDirty} onSavingChange={handleApplicationSaving} /></details></> : null}
        {applicationTarget?.type === "document" ? <DocumentModelForm key={`${selectedTenantID}:${applicationTarget.config.id}`} config={configs.find(config => config.id === applicationTarget.config.id) ?? applicationTarget.config}
          selection={applications.data?.document_selection} loading={applications.loading} busy={applications.saving} error={applications.error} onRefresh={() => void refreshApplications()} onDirtyChange={setApplicationDirty}
          onSave={mode => { void applications.save(mode, applicationTarget.config.id).then(saved => { if (saved) { setApplicationTarget(null); setApplicationDirty(false); message.success(mode === "follow_chat" ? "试卷解析已设为跟随默认配置" : "试卷解析模型已独立设置"); } }); }} /> : null}
        {applicationTarget?.type === "answer_sheet" ? <AnswerSheetModelForm key={`${selectedTenantID}:${applicationTarget.config.id}`} config={configs.find(config => config.id === applicationTarget.config.id) ?? applicationTarget.config}
          selection={applications.data?.answer_sheet_selection} loading={applications.loading} busy={applications.saving} error={applications.error} onRefresh={() => void refreshApplications()} onDirtyChange={setApplicationDirty}
          onSave={mode => { void applications.saveAnswerSheet(mode, applicationTarget.config.id).then(saved => { if (saved) { setApplicationTarget(null); setApplicationDirty(false); message.success(mode === "model" ? "答题卡识别模型已设置" : "已取消答题卡模型配置"); } }); }} /> : null}
      </ModelConfigDrawer>

      <ModelConfigDrawer
        title={editing ? `编辑 ${editing.display_name}` : `为${selectedSchool?.name ?? "学校"}添加模型`}
        width={560}
        open={drawerOpen}
        closable={!saving}
        maskClosable={!saving}
        keyboard={!saving}
        onClose={closeDrawer}
        extra={<Space><Button disabled={saving} onClick={closeDrawer}>关闭</Button><Tooltip title={editingBusy && !saving ? "该模型正在检测或更新，请完成后保存" : undefined}><Button type="primary" loading={saving} disabled={editingBusy && !saving} onClick={() => void save()}>{modelSaveLabel(editing)}</Button></Tooltip></Space>}
        destroyOnHidden
      >
        <ModelConfigForm form={form} editing={editing} credentialSource={credentialSource} watchedSupplier={watchedSupplier}
          changeSupplier={changeSupplier} availableModels={availableModels} modelOptionsOpen={modelOptionsOpen}
          filterAvailableModels={filterAvailableModels} modelsLoading={modelsLoading} discoveryDisabledReason={modelDiscoveryDisabledReason} setAvailableModels={setAvailableModels}
          setModelOptionsOpen={setModelOptionsOpen} setFilterAvailableModels={setFilterAvailableModels} fetchAvailableModels={fetchAvailableModels} onConnectionChange={invalidateModelDiscovery} />
        {latestEditingConfig ? <section className="platform-model-reuse-key">
          <Button disabled={saving || latestEditingConfig.status !== "active" || !latestEditingConfig.credential_configured} onClick={() => {
            // 复用前沿用关闭侧栏的草稿保存与异步失效处理，不能丢弃尚未保存的编辑。
            closeDrawer();
            openCreateWithKey(latestEditingConfig);
          }}>使用此密钥添加模型</Button>
          <small>{latestEditingConfig.status === "active" ? "沿用已保存的供应商、API 地址和密钥，新模型单独保存。" : "启用此配置后可复用密钥添加模型。"}</small>
        </section> : null}
      </ModelConfigDrawer>
    </div>
  );
}
