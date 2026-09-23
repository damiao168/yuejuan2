import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Tag } from "antd";
import { BookOpenText, FileText, LockKeyhole, RefreshCw, ShieldCheck } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import {
  getCurrentRuntimePrompt,
  type RuntimePrompt,
  type RuntimePromptComponent
} from "../../api/modelGovernance";

const commonLabels: Record<string, string> = {
  base: "评分总规则",
  structured: "结构化输出",
  "role.primary": "主评角色",
  "role.arbiter": "仲裁角色",
  "stage.junior": "初中学段",
  "stage.senior": "高中学段"
};

const subjectLabels: Record<string, string> = {
  chinese: "语文",
  mathematics: "数学",
  english: "英语",
  physics: "物理",
  chemistry: "化学",
  biology: "生物",
  history: "历史",
  ethics_politics: "道德与法治 / 思想政治",
  geography: "地理"
};

const questionTypeLabels: Record<string, string> = {
  short_answer: "简答题",
  calculation: "计算题",
  essay: "作文题",
  discussion: "论述题"
};

function promptParts(component?: RuntimePromptComponent) {
  if (!component) return undefined;
  const parts = component.key.split(".");
  return parts.length === 3 && parts[0] === "subject"
    ? { subject: parts[1], questionType: parts[2] }
    : undefined;
}

function promptLabel(component?: RuntimePromptComponent) {
  if (!component) return "系统提示词";
  const parts = promptParts(component);
  if (!parts) return commonLabels[component.key] ?? "其他提示词";
  return `${subjectLabels[parts.subject] ?? "其他学科"} · ${questionTypeLabels[parts.questionType] ?? "其他题型"}`;
}

function promptError(error: unknown) {
  if (error instanceof ApiClientError && error.code === "runtime_prompt_unavailable") {
    return "评分服务当前不可用，无法核对它实际加载的提示词。";
  }
  return getUserErrorMessage(error, "读取系统提示词失败。");
}

export function PromptVersionWorkspace() {
  const [prompt, setPrompt] = useState<RuntimePrompt>();
  const [selected, setSelected] = useState("base");
  const [selectedSubject, setSelectedSubject] = useState("chinese");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const load = async () => {
    setLoading(true);
    setError(undefined);
    try {
      const response = await getCurrentRuntimePrompt();
      setPrompt(response.prompt);
      if (!response.prompt.components.some((item) => item.key === selected)) {
        setSelected(response.prompt.components[0]?.key ?? "base");
      }
    } catch (nextError) {
      setError(promptError(nextError));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
    // Runtime content refreshes only on entry or explicit action.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const active = useMemo(
    () => prompt?.components.find((item) => item.key === selected),
    [prompt, selected]
  );
  const subjectGroups = useMemo(() => {
    const groups = new Map<string, RuntimePromptComponent[]>();
    for (const component of prompt?.components ?? []) {
      const parts = promptParts(component);
      if (!parts) continue;
      groups.set(parts.subject, [...(groups.get(parts.subject) ?? []), component]);
    }
    return [...groups.entries()];
  }, [prompt]);
  const selectedSubjectPrompts = subjectGroups.find(([subject]) => subject === selectedSubject)?.[1] ?? [];

  const selectSubject = (subject: string, components: RuntimePromptComponent[]) => {
    setSelectedSubject(subject);
    setSelected(components[0]?.key ?? "base");
  };

  if (error) {
    return (
      <Alert
        type="warning"
        showIcon
        message="无法读取运行时提示词"
        description={error}
        action={<Button size="small" loading={loading} onClick={() => void load()}>重试</Button>}
      />
    );
  }

  return (
    <div className="prompt-version-workspace" aria-busy={loading}>
      <aside className="prompt-version-index">
        <div className="prompt-version-meta">
          <span>当前运行版本</span>
          <strong>{prompt?.prompt_version ?? "读取中"}</strong>
          <code>{prompt?.bundle_sha256.slice(0, 12) ?? "------------"}</code>
        </div>
        <div className="prompt-common-links" aria-label="公共提示词">
          {(prompt?.components ?? []).filter((component) => !promptParts(component)).map((component) => (
            <button
              type="button"
              key={component.key}
              className={component.key === selected ? "active" : ""}
              onClick={() => setSelected(component.key)}
            >
              {component.key === "base" ? <ShieldCheck size={15} /> : <FileText size={15} />}
              <span>{commonLabels[component.key] ?? "其他提示词"}</span>
              <small>{component.filename}</small>
            </button>
          ))}
        </div>
        <div className="prompt-subject-heading">按学科与题型匹配</div>
        <nav className="prompt-subject-list" aria-label="学科提示词">
          {subjectGroups.map(([subject, components]) => (
            <button
              type="button"
              key={subject}
              className={subject === selectedSubject && promptParts(active ?? components[0]) ? "active" : ""}
              onClick={() => selectSubject(subject, components)}
            >
              <BookOpenText size={15} />
              <span>{subjectLabels[subject] ?? "其他学科"}</span>
              <small>{components.length} 种题型</small>
            </button>
          ))}
        </nav>
      </aside>

      <section className="prompt-version-inspector">
        {promptParts(active ?? selectedSubjectPrompts[0]) && (
          <div className="prompt-question-types" aria-label={`${subjectLabels[selectedSubject] ?? "其他学科"}题型`}>
            {selectedSubjectPrompts.map((component) => {
              const parts = promptParts(component);
              return (
                <button type="button" key={component.key} className={component.key === selected ? "active" : ""} onClick={() => setSelected(component.key)}>
                  {questionTypeLabels[parts?.questionType ?? ""] ?? "其他题型"}
                </button>
              );
            })}
          </div>
        )}
        <header>
          <div>
            <span className="prompt-version-eyebrow">评分服务实际加载内容</span>
            <h2>{promptLabel(active)}</h2>
            <p>{active?.filename ?? "正在读取运行时清单"}</p>
          </div>
          <div className="prompt-version-actions">
            <Tag icon={<LockKeyhole size={12} />} color="blue">运行时只读</Tag>
            <Button icon={<RefreshCw size={15} />} loading={loading} onClick={() => void load()}>刷新</Button>
          </div>
        </header>

        <pre className="prompt-content-viewer">{active?.content ?? "正在从评分服务读取……"}</pre>

        <footer>
          <span>SHA-256</span>
          <code>{active?.sha256 ?? "-"}</code>
        </footer>
        <Alert
          type="info"
          showIcon
          message="提示词不能在生产页面直接热改"
          description="修改需形成新版本，完成固定题集评测和人工批准后随部署清单启用；当前页面展示的是评分服务真正使用的内容，不是说明文档。"
        />
      </section>
    </div>
  );
}
