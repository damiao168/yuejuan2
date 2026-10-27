import { Button, Drawer, Input, Select, Tooltip } from "antd";
import { ArrowUp, Bot, FileText, Menu, Paperclip, Square, X } from "lucide-react";
import { ConversationFlow, ConversationHistory } from "./SchoolConversationDisplay";
import { ConversationImage, canPreviewConversationImage } from "./ConversationImage";
import { conversationModelStatus, conversationPlaceholder } from "./conversationPresentation";
import type { AccountConversationModel } from './useAccountConversation';
import { MAX_ATTACHMENTS, MAX_ACTIVE_CHATS } from './useAccountConversation';

// 展示层只通过账户控制器动作改变流状态；证据与失败恢复继续使用原模块。
export function AccountConversationView({ model: conversation }: Readonly<{ model: AccountConversationModel }>) {
  const { compact, onNavigate, user, threads, activeId, pending, openNew, deleteThread, selectThread, historyOpen, setHistoryOpen, model, chatModels, activePending, selectedModelID, modelLoading, selectModel, activeCount, activeContext, clearContext, activeThread, replyEvidence, failedEvidence, sendErrors, reasoningExpanded, toggleReasoning, copiedMessage, copyReply, retry, modelError, suggestQuestion, scrollFlow, scrollConversation, wheelConversation, send, draftAttachments, setDraftAttachments, inputRef, draft, setDraft, onInputKeyDown, pasteImages, fileInputRef, selectFiles, attaching, stop } = conversation;

  const historyContent = <ConversationHistory threads={threads} activeId={activeId} pending={pending} onNew={openNew}
    onSelect={selectThread} onDelete={deleteThread} />;

  return <div className={`school-chat-shell school-conversation-shell${compact ? " is-compact" : ""}`}>
    <aside className="school-chat-history" aria-label="对话历史">{historyContent}</aside>
    <Drawer title="对话历史" placement="left" open={historyOpen} onClose={() => setHistoryOpen(false)} width={280} styles={{ body: { padding: 0 } }} className="school-chat-mobile-drawer">
      <div className="school-chat-history school-chat-history-mobile">{historyContent}</div>
    </Drawer>

    <section className="school-chat-main" aria-label="AI 对话内容">
      <header className="school-chat-topbar">
        <div className="school-chat-topbar-title"><Button className="school-chat-mobile-menu" type="text" icon={<Menu size={18} />} aria-label="打开对话历史" onClick={() => setHistoryOpen(true)} /><Bot size={21} /><span>学校 AI 助手</span></div>
        <div className="school-chat-model-status"><i className={model?.available ? "is-online" : ""} />{chatModels.length ? <Select aria-label="切换对话模型" size="small" style={{ width: "min(300px, 40vw)" }} value={activePending?.modelConfigID ?? selectedModelID} disabled={modelLoading || Boolean(activePending)}
          options={chatModels.map(item => ({ value: item.id, label: `${item.display_name} · ${item.model_name}${item.available ? "" : "（不可用）"}`, disabled: !item.available }))}
          onChange={selectModel} /> : conversationModelStatus(model, modelLoading)}{activeCount > 0 ? ` · ${activeCount}/${MAX_ACTIVE_CHATS} 个对话进行中` : ""}</div>
      </header>
      {activeContext?.exam_id ? <div className="school-conversation-context"><span>当前考试<strong>{activeContext.exam_name || activeContext.exam_id}</strong></span><Button type="text" size="small" icon={<X size={13} />} aria-label="移除当前考试上下文" disabled={Boolean(activePending)} onClick={clearContext}>移除</Button></div> : null}

      <ConversationFlow thread={activeThread} pending={activePending} evidence={replyEvidence}
        failedEvidence={activeThread && failedEvidence[activeThread.id]} sendError={activeThread && sendErrors[activeThread.id]}
        user={user} onNavigate={onNavigate} reasoningExpanded={reasoningExpanded}
        onReasoningToggle={toggleReasoning} copiedMessage={copiedMessage} onCopy={copyReply} retryDisabled={activeCount >= MAX_ACTIVE_CHATS} onRetry={retry}
        modelError={modelError} model={model} onSuggestion={suggestQuestion} scrollRef={scrollFlow}
        onScroll={scrollConversation} onWheel={wheelConversation} />

      <div className="school-chat-composer-wrap">
        <form className="school-chat-composer" onSubmit={send}>
          {draftAttachments.length > 0 ? <div className="school-chat-attachments">{draftAttachments.map((attachment, index) => <span className={attachment.media_type.startsWith("image/") ? "is-image" : undefined} title={`${attachment.name} · ${Math.max(1, Math.round(attachment.size / 1024))} KB`} key={`${attachment.name}-${attachment.size}-${index}`}>
            {canPreviewConversationImage(attachment) ? <ConversationImage attachment={attachment} /> : <FileText size={15} />}<b title={attachment.name}>{attachment.name}</b><small>{Math.max(1, Math.round(attachment.size / 1024))} KB</small><button type="button" aria-label={`移除附件：${attachment.name}`} onClick={() => setDraftAttachments((current) => current.filter((item) => item !== attachment))}><X size={13} /></button></span>)}</div> : null}
          <Input.TextArea
            ref={inputRef} value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={onInputKeyDown} onPaste={pasteImages}
            placeholder={conversationPlaceholder(Boolean(model?.available), activeCount >= MAX_ACTIVE_CHATS, Boolean(activePending))} disabled={!model?.available || Boolean(activePending) || activeCount >= MAX_ACTIVE_CHATS}
            autoSize={{ minRows: 2, maxRows: 7 }} maxLength={20_000} aria-label="对话输入"
          />
          <div className="school-chat-composer-bottom">
            <input ref={fileInputRef} className="school-chat-file-input" type="file" multiple accept=".png,.jpg,.jpeg,image/png,image/jpeg,.pdf,.txt,.md,.csv,.json,.xml,.html,.css,.js,.jsx,.ts,.tsx,.py,.java,.go,.sql,.yaml,.yml,.log,.ini,.conf,text/*,application/pdf,application/json" onChange={selectFiles} />
            <Tooltip title="可直接粘贴 PNG、JPEG 图片，每张不超过 3 MB；支持 PDF 和文本，共最多 3 个附件"><Button type="text" size="small" icon={<Paperclip size={17} />} loading={attaching} disabled={Boolean(activePending) || activeCount >= MAX_ACTIVE_CHATS || draftAttachments.length >= MAX_ATTACHMENTS} onClick={() => fileInputRef.current?.click()}>上传文件</Button></Tooltip>
            {activePending ? <Button type="primary" shape="circle" icon={<Square size={15} fill="currentColor" />} aria-label="中断当前对话" onClick={stop} /> : <Button type="primary" shape="circle" htmlType="submit" icon={<ArrowUp size={19} />} aria-label="发送消息" disabled={(!draft.trim() && draftAttachments.length === 0) || !model?.available || activeCount >= MAX_ACTIVE_CHATS || attaching} />}
          </div>
        </form>
        <p className="school-chat-disclaimer">业务回答以本轮来源为依据；正式操作请进入业务页面核实。</p>
      </div>
    </section>
  </div>;
}
