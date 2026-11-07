import type {
CreateQuestionBankItemRequest,
CreateQuestionBankRequest,
QuestionBankItem,
QuestionBankScoring,
QuestionBankVersion,
UpdateQuestionBankVersionRequest,
} from "@edugrade/sdk";
import {
Empty
} from "antd";
import { ApiClientError,getUserErrorMessage } from "../../api/client";
import { questionBankApi } from "../../api/questionBank";
import { ErrorState } from "../../components/PageState";
import { QuestionBankConflictDialog } from "./QuestionBankConflictDialog";
import { QuestionBankDialogs } from "./QuestionBankDialogs";
import { QuestionBankHeader } from "./QuestionBankHeader";
import { QuestionBankWorkspace } from "./QuestionBankWorkspace";
import "./question-bank.css";
import { mergeQuestionBankDraft } from "./questionBankDraftMerge";
import { contentFrom,defaults,transitionSuccessMessages } from "./questionBankPageState";
import { emptySearch } from "./questionBankTypes";
import { useQuestionBankAdministration } from "./useQuestionBankAdministration";
import { useQuestionBankEditorData } from "./useQuestionBankEditorData";

export function QuestionBankPage(props: Parameters<typeof useQuestionBankEditorData>[0]) {
  const { user, message, modal, client, prefix, bankId, setBankId, item, setItem, versionId, setVersionId, kind, setKind, editorVersion, setEditorVersion, aclOpen, setAclOpen, schemaOpen, setSchemaOpen, searchOpen, setSearchOpen, searchContainer, setSearchContainer, searchFilters, setSearchFilters, searchOffset, setSearchOffset, comment, setComment, contentDirty, setContentDirty, scoringDirty, setScoringDirty, setQuery, offset, setOffset, bankOffset, setBankOffset, versionOffset, setVersionOffset, dialog, setDialog, saving, setSaving, conflict, setConflict, latestPreview, setLatestPreview, scoringResetToken, setScoringResetToken, restoredScoringDraft, setRestoredScoringDraft, scoringDraft, scoringSubmit, registerScoringSubmit, editor, aclForm, schemaForm, setSchemaFormBankId, setSchemaReadFailureBankId, schemaContext, pending, loadedVersion, editorGeneration, displayedEditorGeneration, canCreate, canEdit, canManage, canRetire, banks, schools, items, versions, version, versionMismatch, selected, currentSchema, boundSchema, acl, searchResults, reviews, linkedItem, bank, schemaReadable, schemaFormReady, schemaLoadFailed, members } = useQuestionBankEditorData(props);
	const { saveACL, saveSchema } = useQuestionBankAdministration({
		bankId, bank, actorId: user.id, acl, currentSchema, members: members.data?.members ?? [],
		aclOpen, schemaOpen, aclForm, schemaForm, schemaContext, saving, setSaving, setAclOpen, setSchemaOpen,
		setSchemaFormBankId, setSchemaReadFailureBankId, command, refresh,
	});
	// 未保存的题目编辑只在当前表单里；切换范围前要由用户决定是否丢弃。
	function confirmDiscard(action: () => void, content = "当前题目内容或评分标准尚未保存，切换后无法恢复。") {
		if (!contentDirty && !scoringDirty) return action();
		modal.confirm({
			title: "放弃未保存的修改？",
			content,
			okText: "放弃并切换",
			okButtonProps: { danger: true },
			cancelText: "继续编辑",
			onOk: action,
		});
	}
	function selectBank(id: string) {
		if (id === bankId) return;
		confirmDiscard(() => applyBankSelection(id));
	}
	function applyBankSelection(id: string) {
    editorGeneration.current += 1; setSaving(false);
		schemaContext.current = { bankId: id, readable: false, formReady: false };
		setSchemaFormBankId(null);
		loadedVersion.current = null;
		setBankId(id);
		setItem(null);
		setVersionId("");
		setOffset(0);
		setQuery("");
		setSearchFilters(emptySearch);
		setConflict(false);
	}
	function selectItem(next: QuestionBankItem, nextVersionId = "", onSelected?: () => void) {
		if (next.id === item?.id && (!nextVersionId || nextVersionId === versionId)) {
			onSelected?.();
			return;
		}
		confirmDiscard(() => {
			applyItemSelection(next, nextVersionId);
			onSelected?.();
		});
	}
	function applyItemSelection(next: QuestionBankItem, nextVersionId = "") {
    editorGeneration.current += 1; setSaving(false);
		loadedVersion.current = null;
		setItem(next);
		setVersionId(nextVersionId);
		setVersionOffset(0);
		setConflict(false);
	}
	function selectVersion(id: string, nextOffset?: number) {
		if (id === versionId && nextOffset === undefined) return;
		confirmDiscard(() => {
			if (nextOffset !== undefined) setVersionOffset(nextOffset);
      editorGeneration.current += 1; setSaving(false);
      loadedVersion.current = null;
			setVersionId(id);
		});
	}
	async function command<T>(
		operation: string,
		payload: unknown,
		execute: (headers: { "Idempotency-Key": string }) => Promise<T>,
	): Promise<T> {
		const fingerprint = JSON.stringify({ operation, payload });
		if (pending.current?.fingerprint !== fingerprint)
			pending.current = { fingerprint, key: crypto.randomUUID() };
		const reservation = pending.current;
		const result = await execute({ "Idempotency-Key": reservation.key });
		if (pending.current === reservation) pending.current = null;
		return result;
	}
	async function refresh() {
		await client.invalidateQueries({ queryKey: prefix });
	}
	async function createBank(values: CreateQuestionBankRequest) {
		setSaving(true);
		try {
			const result = await command("create-bank", values, (headers) =>
				questionBankApi.createQuestionBank({ body: values, headers }),
			);
			setDialog(null);
			setBankOffset(0);
			selectBank(result.bank.id);
			await refresh();
			message.success("题库已创建");
		} catch (error) {
			message.error(getUserErrorMessage(error, "创建题库失败"));
		} finally {
			setSaving(false);
		}
	}
	async function createItem(values: CreateQuestionBankItemRequest) {
		if (saving || schemaContext.current.bankId !== bankId || !schemaContext.current.readable) {
			message.warning("请先成功读取当前题库的分类规则，再创建题目草稿。");
			return;
		}
		values = {
			...values,
			options: values.options?.filter((option) => option.trim()) ?? [],
		};
		setSaving(true);
		try {
			const result = await command(
				`create-item:${bankId}`,
				values,
				(headers) =>
					kind === "rubric_template"
						? questionBankApi.createQuestionBankRubricTemplate({
								body: { ...values, bank_id: bankId },
								headers,
							})
						: questionBankApi.createQuestionBankItem({
								path: { bankId },
								body: values,
								headers,
							}),
			);
			setDialog(null);
			setOffset(0);
			selectItem(result.item);
			await refresh();
			message.success(
				kind === "rubric_template" ? "模板草稿已创建" : "题目草稿已创建",
			);
		} catch (error) {
			message.error(
				getUserErrorMessage(
					error,
					kind === "rubric_template" ? "创建模板失败" : "创建题目失败",
				),
			);
		} finally {
			setSaving(false);
		}
	}
	async function saveVersion(values: UpdateQuestionBankVersionRequest) {
		if (!selected) return;
    const generation = editorGeneration.current;
		values = {
			...values,
			options: values.options?.filter((option) => option.trim()) ?? [],
		};
		setSaving(true);
		try {
			const result = await command(
				`update-version:${selected.id}`,
				values,
				(headers) =>
					questionBankApi.updateQuestionBankVersion({
						path: { versionId: selected.id },
						body: values,
						headers,
					}),
			);
			if (loadedVersion.current === result.version.id && generation === editorGeneration.current) {
				setContentDirty(false);
				setEditorVersion(result.version);
				editor.setFieldsValue({
					...contentFrom(result.version),
					expected_revision: result.version.revision,
				});
			}
			await refresh();
			message.success("草稿已保存");
		} catch (error) {
			if (generation !== editorGeneration.current) return;
			if (error instanceof ApiClientError && error.status === 409)
				setConflict(true);
			message.error(getUserErrorMessage(error, "保存草稿失败"));
		} finally {
			if (generation === editorGeneration.current) setSaving(false);
		}
	}
  async function saveCombinedScoring(scoring: QuestionBankScoring) {
    if (!editorVersion || saving || conflict) throw new Error("请先核对当前草稿状态");
    const target = editorVersion.id;
    const generation = editorGeneration.current;
    const isCurrent = () => generation === editorGeneration.current && loadedVersion.current === target;
    setSaving(true);
    try {
      const values = await editor.validateFields();
      if (!isCurrent()) throw new Error("题目已切换，请在当前草稿重新保存");
      const body: UpdateQuestionBankVersionRequest = {
        ...values, expected_revision: editorVersion.revision,
        options: values.options?.filter((option: string) => option.trim()) ?? [], scoring
      };
      // 单个命令同时落盘两区；网络重试沿用同一输入与幂等键，不能先保存一半再推进修订。
      const result = await command(`update-version:${target}`, body, headers => questionBankApi.updateQuestionBankVersion({ path: { versionId: target }, body, headers }));
      if (isCurrent()) {
        setContentDirty(false); setScoringDirty(false);
        client.setQueryData([...prefix, "version", target], result);
        editor.setFieldsValue({ ...contentFrom(result.version), expected_revision: result.version.revision });
        setEditorVersion(result.version);
      }
      return result.version;
    } catch (error) {
      if (isCurrent() && error instanceof ApiClientError && error.status === 409) setConflict(true);
      throw error;
    } finally { if (isCurrent()) setSaving(false); }
  }
	async function loadLatest() {
		if (!selected || saving) return;
		const requestVersionId = selected.id;
    const generation = editorGeneration.current;
		setSaving(true);
		try {
			// 预览读取不更新查询缓存或编辑修订号，只有用户明确采用后才替换本地草稿。
			const result = await questionBankApi.getQuestionBankVersion({ path: { versionId: requestVersionId } });
			if (loadedVersion.current === requestVersionId && generation === editorGeneration.current) setLatestPreview(result.version);
		} catch (error) {
			message.error(getUserErrorMessage(error, "加载最新版本失败"));
		} finally {
			if (generation === editorGeneration.current) setSaving(false);
		}
	}
	function acceptLatest() {
		if (latestPreview?.id !== loadedVersion.current) return;
    editorGeneration.current += 1;
		setEditorVersion(latestPreview);
		setRestoredScoringDraft(undefined);
		editor.setFieldsValue({ ...contentFrom(latestPreview), expected_revision: latestPreview.revision });
		client.setQueryData([...prefix, "version", latestPreview.id], { version: latestPreview });
		setContentDirty(false); setScoringDirty(false); setConflict(false); setLatestPreview(null);
		// 即使服务器修订未变化，明确放弃也须重建评分表单，不能只把脏标记清除。
		setScoringResetToken((value) => value + 1);
		message.success("已采用最新版本，本地未保存修改已放弃。");
	}
  function mergeLatest(localGroups: ReadonlySet<string>) {
    if (!canEdit || !editorVersion || !latestPreview || latestPreview.id !== loadedVersion.current || saving) return;
    try {
      const merged = mergeQuestionBankDraft(editorVersion, latestPreview, editor.getFieldsValue(true), scoringDraft.current, localGroups);
      editorGeneration.current += 1;
      // 采用最新修订作为后续保存前提，不向服务器写入；别人再修改时仍会正常触发 409。
      setEditorVersion(latestPreview);
      client.setQueryData([...prefix, "version", latestPreview.id], { version: latestPreview });
      editor.setFieldsValue({ ...merged.content, expected_revision: latestPreview.revision });
      setRestoredScoringDraft(merged.scoring);
      scoringDraft.current = merged.scoring;
      setContentDirty(true); setScoringDirty(true); setConflict(false); setLatestPreview(null);
      setScoringResetToken(value => value + 1);
      message.success("合并草稿已生成，请核对后保存。");
    } catch (error) { message.error(getUserErrorMessage(error, "无法合并当前版本，请重新读取最新草稿")); }
  }
	function downloadLocalDraft() {
		if (!editorVersion) return;
		const blob = new Blob([JSON.stringify({ version_id: editorVersion.id, saved_revision: editorVersion.revision, content: editor.getFieldsValue(true), scoring: scoringDraft.current ?? editorVersion.scoring }, null, 2)], { type: "application/json" });
		const url = URL.createObjectURL(blob);
		const link = document.createElement("a"); link.href = url; link.download = `题目本地修改备份-${item?.item_code ?? "草稿"}.json`; link.click();
		window.setTimeout(() => URL.revokeObjectURL(url), 1000);
	}
	function scoringSaved(v: QuestionBankVersion, generation = editorGeneration.current) {
    // 评分保存有独立的等待状态；即使用户已换题，旧表单的成功回执也不能重建当前内容表单。
    if (loadedVersion.current !== v.id || generation !== editorGeneration.current) return;
		setRestoredScoringDraft(undefined);
		setEditorVersion(v);
		editor.setFieldsValue({ ...contentFrom(v), expected_revision: v.revision });
		void refresh();
	}
	function currentHandoffRevision(v: QuestionBankVersion) {
		return selected?.id === v.id && selected.revision === v.revision && selected.bundle_hash === v.bundle_hash ? selected.handoff?.revision ?? 0 : v.handoff?.revision ?? 0;
	}
	async function transition(
		decision: "submit-review" | "approve" | "return-to-draft" | "publish",
		assignees?: { reviewer_id: string; publisher_id: string },
	) {
		if (!editorVersion || saving) return;
    const generation = editorGeneration.current;
		setSaving(true);
		const body = {
			expected_revision: editorVersion.revision,
			bundle_hash: editorVersion.bundle_hash,
			comment,
			expected_handoff_revision: currentHandoffRevision(editorVersion),
			...assignees,
		};
		try {
			const methods = {
				"submit-review":
					questionBankApi.submitQuestionBankReview.bind(questionBankApi),
				approve:
					questionBankApi.approveQuestionBankVersion.bind(questionBankApi),
				"return-to-draft":
					questionBankApi.returnQuestionBankVersionToDraft.bind(
						questionBankApi,
					),
				publish:
					questionBankApi.publishQuestionBankVersion.bind(questionBankApi),
			};
			const result = await command(
				`${decision}:${editorVersion.id}`,
				body,
				(headers) =>
					methods[decision]({
						path: { versionId: editorVersion.id },
						body,
						headers,
					}),
			);
			if (generation === editorGeneration.current) {
        scoringSaved(result.version, generation);
        setComment("");
        message.success(transitionSuccessMessages[decision]);
      }
		} catch (error) {
			if (generation !== editorGeneration.current) return;
			if (error instanceof ApiClientError && error.status === 409)
				setConflict(true);
			message.error(
				getUserErrorMessage(error, "版本操作失败，请核对授权与评分标准"),
			);
		} finally {
			if (generation === editorGeneration.current) setSaving(false);
		}
	}
	async function updateHandoff(action: "assign" | "review" | "publish", assignees?: { reviewer_id: string; publisher_id: string }) {
		if (!editorVersion || saving) return;
		const versionId = editorVersion.id;
		const generation = editorGeneration.current;
		const body = { expected_revision: editorVersion.revision, expected_handoff_revision: currentHandoffRevision(editorVersion) };
		setSaving(true);
		try {
			const result = await command(`handoff:${action}:${versionId}`, { ...body, ...assignees }, headers => {
				if (action === "assign") {
					if (!assignees) throw new Error("请选择审核与发布教师");
					return questionBankApi.assignQuestionBankHandoff({ path: { versionId }, body: { ...body, ...assignees }, headers });
				}
				return action === "review" ? questionBankApi.claimQuestionBankReview({ path: { versionId }, body, headers }) : questionBankApi.claimQuestionBankPublish({ path: { versionId }, body, headers });
			});
			// 同一题离开后再进入也有新世代；旧领取回执不能覆盖新页面的交接状态。
			if (generation === editorGeneration.current) {
				// 交接不编辑题干或评分；成功回执只推进交接投影，不能重填表单而丢掉本地修改。
				setEditorVersion(current => current?.id === result.version.id && current.revision === result.version.revision && current.bundle_hash === result.version.bundle_hash ? { ...current, handoff: result.version.handoff, workflow_status: result.version.workflow_status } : current);
				client.setQueryData([...prefix, "version", versionId], result);
				void refresh();
				message.success(action === "assign" ? "交接人员已更新" : "已领取本题待办");
			}
		} catch (error) {
			if (generation === editorGeneration.current) message.error(getUserErrorMessage(error, "交接未完成，请读取最新版本后重试"));
		} finally {
			if (generation === editorGeneration.current) setSaving(false);
		}
	}
	function derive() {
		if (!selected || saving) return;
		const sourceVersionId = selected.id;
		// 派生复制服务端已保存版本；内容和评分两个草稿都必须先经明确丢弃确认。
		confirmDiscard(() => {
			if (loadedVersion.current === sourceVersionId) void deriveSavedVersion();
		}, "新版本会复制当前已保存的内容和评分标准；未保存的题目或评分修改不会进入新版本，放弃后无法恢复。");
	}
	async function deriveSavedVersion() {
		if (!item || !selected) return;
		setSaving(true);
		try {
			const result = await command(
				`derive:${item.id}`,
				{ source_version_id: selected.id },
				(headers) =>
					questionBankApi.createQuestionBankVersion({
						path: { itemId: item.id },
						body: { source_version_id: selected.id },
						headers,
					}),
			);
			setVersionOffset(0);
			setVersionId(result.version.id);
			await refresh();
			message.success("新版本草稿已创建");
		} catch (error) {
			message.error(getUserErrorMessage(error, "创建新版本失败"));
		} finally {
			setSaving(false);
		}
	}
	function confirmRetireItem() {
		if (!item || saving) return;
		modal.confirm({title:`停止选用题目 ${item.item_code}？`,content:"此题将从新考试的候选题中移除，不能再次选入考试。已有考试保存的题目版本和历史成绩继续保留。当前不支持恢复选用；如需再用请新建题目。",okText:"确认停止选用",cancelText:"继续保留",okButtonProps:{danger:true},onOk:retireItem});
	}
	async function retireItem() {
		if (!item) return;
		setSaving(true);
		try {
			await command(`retire:${item.id}`, { expected_revision: item.revision }, (headers) =>
				questionBankApi.retireQuestionBankItem({
					path: { itemId: item.id },
					body: { expected_revision: item.revision },
					headers,
				}),
			);
			setItem(null);
			setVersionId("");
			await refresh();
			message.success("已停止选用；已有考试和历史成绩保留");
		} catch (error) {
			message.error(getUserErrorMessage(error, "停止选用失败，题目仍保留"));
		} finally {
			setSaving(false);
		}
	}
	const schoolOptions =
		schools.data?.schools
			.filter((s) => s.status === "active")
			.map((s) => ({ value: s.id, label: s.name })) ??
		user.organizationScope.schoolIds.map((id) => ({
			value: id,
			label: user.school || "所属学校",
		}));
	if (banks.isError)
		return (
			<ErrorState
				message={getUserErrorMessage(banks.error, "题库加载失败")}
				onRetry={() => void banks.refetch()}
			/>
		);
	return (
		<div className="question-bank-page">
			{linkedItem.isError ? <ErrorState message="这道题目暂不可打开。请核对题库授权，或联系题库负责人；其他题库仍可浏览。" onRetry={()=>void linkedItem.refetch()} /> : null}
            {versionMismatch ? <ErrorState message="链接中的版本不属于当前题目，已停止显示和编辑。请打开本题最新版本。" onRetry={()=>setVersionId(versions.data?.versions[0]?.id ?? "")} /> : null}
			<QuestionBankHeader canCreate={canCreate} loading={banks.isLoading} banks={banks.data?.banks} bankId={bankId}
				selectBank={selectBank} bankStatus={bank?.status} kind={kind} bankOffset={bankOffset} total={banks.data?.total ?? 0}
				onRefresh={() => void refresh()} onCreateBank={() => setDialog("bank")} onCreateItem={() => setDialog("item")}
				onSearchOpen={() => setSearchOpen(true)} onSearch={(value) => { setQuery(value); setSearchFilters(current => ({ ...current, q: value })); setOffset(0); }}
				onBankPageChange={(page) => confirmDiscard(() => { setBankOffset((page - 1) * 20); applyBankSelection(""); })} />
      <div ref={setSearchContainer} />
			{!banks.isLoading && !banks.data?.total ? (
				<Empty description="尚无获授权的题库，可新建题库开始维护题目。" />
			) : (
				<QuestionBankWorkspace
					bank={bank} kind={kind} onKindChange={(value) => confirmDiscard(() => {
            editorGeneration.current += 1; setSaving(false);
						setKind(value);
						setItem(null);
						setVersionId("");
						setEditorVersion(null);
						setOffset(0);
						loadedVersion.current = null;
					})}
					canManage={canManage} openSchema={() => setSchemaOpen(true)} openAcl={() => setAclOpen(true)}
					items={items} selectItem={selectItem} offset={offset} setOffset={setOffset}
					item={item} selected={selected} versions={versions} version={version}
					versionOffset={versionOffset} setVersionId={selectVersion}
					saving={saving} loadLatest={() => void loadLatest()} canEdit={canEdit} derive={() => void derive()}
					canRetire={canRetire} retireItem={confirmRetireItem} boundSchema={boundSchema}
					editor={editor} saveVersion={(values) => void saveVersion(values)} contentDirty={contentDirty} setContentDirty={setContentDirty}
					scoringDirty={scoringDirty} conflict={conflict} editorVersion={editorVersion}
					scoringSaved={saved => scoringSaved(saved, displayedEditorGeneration)} setScoringDirty={setScoringDirty} scoringResetToken={scoringResetToken} restoredScoringDraft={restoredScoringDraft} onScoringDraftSnapshot={(draft) => { scoringDraft.current = draft; }} user={user}
					comment={comment} setComment={setComment} transition={(decision, assignees) => void transition(decision, assignees)} handoff={(action, assignees) => void updateHandoff(action, assignees)} reviews={reviews}
					members={members}
          saveAllDraft={() => scoringSubmit.current?.()} saveCombinedScoring={saveCombinedScoring} registerScoringSubmit={registerScoringSubmit}
				/>
			)}
			<QuestionBankConflictDialog original={editorVersion} latest={latestPreview} localContent={editor.getFieldsValue(true)} scoringDraft={scoringDraft.current}
				dirty={contentDirty || scoringDirty} onClose={() => setLatestPreview(null)} onDownload={downloadLocalDraft} onAccept={acceptLatest} onMerge={canEdit ? mergeLatest : undefined} />
			<QuestionBankDialogs
				bankId={bankId} dialog={dialog} setDialog={setDialog} saving={saving} schoolOptions={schoolOptions} schools={schools} createBank={(values) => void createBank(values)}
				aclOpen={aclOpen} setAclOpen={setAclOpen} aclForm={aclForm} saveACL={(values) => void saveACL(values)} acl={acl}
				protectedMemberIds={[user.id, bank?.created_by ?? ""]}
				members={members}
				schemaOpen={schemaOpen} setSchemaOpen={setSchemaOpen} currentSchema={currentSchema} schemaReadable={schemaReadable} schemaFormReady={schemaFormReady} schemaLoadFailed={schemaLoadFailed} schemaForm={schemaForm} saveSchema={(values) => void saveSchema(values)}
				searchOpen={searchOpen} setSearchOpen={setSearchOpen} searchFilters={searchFilters} setSearchFilters={setSearchFilters} setSearchOffset={setSearchOffset}
				showSearchResults={false}
				searchContainer={searchContainer}
					searchOffset={searchOffset} searchResults={searchResults} selectItem={selectItem} kind={kind} createItem={(values) => void createItem(values)} defaults={defaults}
			/>
		</div>
	);
}
