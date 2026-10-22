import { Button } from "antd";
import { GradingAIPlanSettings } from "./GradingAIPlanSettings";

interface GradingSettingsViewProps {
  examId: string;
  questionId: string;
  onOpenGoldPapers: () => void;
  onOpenCalibration: (questionId: string) => void;
  onAIPlanSaved?: () => void;
}

export function GradingSettingsView({ examId, questionId, onOpenGoldPapers, onOpenCalibration, onAIPlanSaved }: Readonly<GradingSettingsViewProps>) {
  return <section className="grading-settings-workspace" aria-label="阅卷设置">
    <h1>阅卷设置</h1>
    <GradingAIPlanSettings examId={examId} onSaved={onAIPlanSaved} />
    <div className="grading-setting-item"><div><h2>标准卷</h2><p>维护阅卷参考样卷。</p></div><Button onClick={onOpenGoldPapers}>管理标准卷</Button></div>
    <div className="grading-setting-item"><div><h2>阅卷前试评</h2><p>正式阅卷前，统一评分尺度。</p></div><Button disabled={!questionId} title={!questionId ? "读取题目后可开始试评" : undefined} onClick={() => onOpenCalibration(questionId)}>进入试评</Button></div>
  </section>;
}
