import { useState } from "react";
import { App } from "antd";
import { setExamAttendance, type RosterEntry, type RosterReport } from "../../../api/scores";

type RunAction = (key: string, action: () => Promise<void>, successText: string) => Promise<void>;

export function useRosterAttendance({
  selectedExamId,
  setRoster,
  runAction
}: {
  selectedExamId: string;
  setRoster: (roster: RosterReport | null) => void;
  runAction: RunAction;
}) {
  const { message } = App.useApp();
  const [attendanceEditor, setAttendanceEditor] = useState<{ entry: RosterEntry; status: "expected" | "absent" } | null>(null);
  const [attendanceReason, setAttendanceReason] = useState("");

  const openAttendanceEditor = (entry: RosterEntry, status: "expected" | "absent") => {
    setAttendanceEditor({ entry, status });
    setAttendanceReason("");
  };

  const closeAttendanceEditor = () => {
    setAttendanceEditor(null);
    setAttendanceReason("");
  };

  const saveAttendance = async () => {
    if (!selectedExamId || !attendanceEditor?.entry.student_id || !attendanceReason.trim()) {
      message.error("请填写本次名册调整原因");
      return;
    }
    await runAction(
      "attendance",
      async () => {
        const result = await setExamAttendance(
          selectedExamId,
          attendanceEditor.entry.student_id!,
          attendanceEditor.status,
          attendanceReason.trim()
        );
        setRoster(result.roster);
        setAttendanceEditor(null);
        setAttendanceReason("");
      },
      attendanceEditor.status === "absent" ? "已标记缺考" : "已恢复为应考"
    );
  };

  return {
    attendanceEditor,
    attendanceReason,
    setAttendanceReason,
    openAttendanceEditor,
    closeAttendanceEditor,
    saveAttendance
  };
}
