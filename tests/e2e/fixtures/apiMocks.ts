import type { Page, Route } from "@playwright/test";
import roleMatrix from "../../../contracts/authorization/role-matrix.json" with { type: "json" };
import type { ManagementExam } from "../../../apps/web-admin/src/api/exams";

type TestRole = "school_admin" | "platform_admin" | "teacher" | "grader" | "arbitrator";

const rolePermissions = Object.fromEntries(Object.entries(roleMatrix).map(([role, contract]) => [role, contract.permissions])) as Record<TestRole, string[]>;

function userFor(role: TestRole) {
  return {
    id: `user-${role}`,
    tenant_id: role === "platform_admin" ? "platform" : "tenant-school",
    tenant_code: role === "platform_admin" ? "platform" : "demo-school",
    username: role,
		display_name: role === "school_admin" ? "学校管理员" : role === "teacher" ? "学科教师" : role === "grader" ? "阅卷老师" : role === "arbitrator" ? "仲裁教师" : "平台管理员",
    status: "active",
    roles: [role],
    permissions: rolePermissions[role],
		data_scope: role === "platform_admin"
			? { platform_admin: { scope: "platform" } }
			: { [role]: { scope: roleMatrix[role].scope, ...(role === "school_admin" ? { school_id: "school-1", school_name: "示范学校" } : {}) } },
		organization_scope: role === "platform_admin"
			? { tenant_wide: true, school_ids: [], grade_ids: [], class_ids: [] }
			: { tenant_wide: false, school_ids: ["school-1"], grade_ids: role === "teacher" ? ["grade-1"] : [], class_ids: role === "teacher" ? ["class-1"] : [] }
	};
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body)
  });
}

// 统一模拟权限和页面数据；专用夹具应后注册并 fallback 到这里，未配置接口统一报错以暴露漏配。
export async function installApiMocks(
  page: Page,
  options: { role?: TestRole; initiallyAuthenticated?: boolean; dashboardMode?: "default" | "empty"; onboardingReady?: boolean; sessionSubjects?: boolean } = {}
) {
  const role = options.role ?? "school_admin";
  const user = userFor(role);
  // 空看板与学校初始化未完成是两种状态；专用用例可独立指定启用状态，旧夹具默认行为保持不变。
  const onboardingReady = options.onboardingReady ?? options.dashboardMode !== "empty";
  let authenticated = options.initiallyAuthenticated ?? false;
  const managementExam: ManagementExam = {
    id: options.sessionSubjects ? "session-1" : "exam-1", kind: options.sessionSubjects ? "session" : "standalone",
    school_id: "school-1", grade_id: "grade-1", name: options.sessionSubjects ? "2026 春季期中考试" : "2026 春季数学期中考试",
    exam_type: "midterm_exam", revision: 3, created_by: "user-school_admin", created_at: "2026-08-01T00:00:00Z",
    class_ids: ["class-1"], default_class_ids: ["class-1"], inherited_subject_ids: options.sessionSubjects ? ["exam-1", "exam-physics"] : ["exam-1"],
    scope_complete: true, can_manage: true, can_edit: true, can_archive: false, can_restore: false,
    exams: (options.sessionSubjects ? [{ id: "exam-1", subject: "math", total_score: 150 }, { id: "exam-physics", subject: "physics", total_score: 100 }] : [{ id: "exam-1", subject: "math", total_score: 150 }]).map((subject) => ({
      ...subject, tenant_id: "tenant-school", school_id: "school-1", name: subject.subject === "physics" ? "2026 春季物理期中考试" : "2026 春季数学期中考试",
      exam_type: "midterm_exam", status: "collecting", grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "manual_publish",
      created_by: "user-school_admin", class_ids: ["class-1"], revision: 3, created_at: "2026-08-01T00:00:00Z",
      ...(options.sessionSubjects ? { exam_session_id: "session-1", exam_session_name: "2026 春季期中考试", exam_session_grade_id: "grade-1" } : {})
    })),
    progress: { status: "collecting", label: "答卷导入中", total_count: options.sessionSubjects ? 2 : 1, ready_count: options.sessionSubjects ? 2 : 1, published_count: 0, counts: { collecting: options.sessionSubjects ? 2 : 1 }, facts_available: true },
    archive_blockers: [{ code: "exam_in_progress", message: "考试仍在答卷导入阶段" }]
  };
  let createdManagementExam: ManagementExam | null = null;
  const managementReceipts = new Map<string, { request: string; exam: ManagementExam }>();

  await page.route(/\/api\/v1(?:\/|$)/, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;

    if (path === "/api/v1/auth/me") {
      return authenticated
        ? json(route, { user })
        : json(route, { error: { code: "unauthorized", message: "请先登录" } }, 401);
    }
    if (path === "/api/v1/auth/login") {
      authenticated = true;
      return json(route, {
        expires_at: "2099-01-01T00:00:00Z",
        user
      });
    }
    if (path === "/api/v1/auth/logout") return json(route, { status: "ok" });
    if (path === "/api/v1/auth/mfa" && request.method() === "GET") return json(route, { available: true, enabled: false, recovery_codes_remaining: 0 });
    if (path === "/api/v1/auth/sessions" && request.method() === "GET") return json(route, { sessions: [
      { id: "session-current", session_type: "remembered_device", device_name: "办公室 Windows 电脑", created_at: "2026-09-20T08:00:00Z", last_seen_at: "2026-09-20T12:00:00Z", expires_at: "2026-10-20T08:00:00Z", current: true },
      { id: "session-tablet", session_type: "standard", device_name: "教务处平板", created_at: "2026-09-19T08:00:00Z", last_seen_at: "2026-09-19T10:00:00Z", expires_at: "2026-09-21T08:00:00Z", current: false }
    ] });
    if (path === "/api/v1/auth/security-events" && request.method() === "GET") return json(route, { events: [
      { id: "security-login", event_type: "auth.login_succeeded", risk_level: "low", device_summary: "Windows 设备", occurred_at: "2026-09-20T11:59:00Z" }
    ] });
    if (path.startsWith("/api/v1/auth/sessions/") && request.method() === "DELETE") return json(route, { status: "revoked" });
    if (path === "/api/v1/auth/logout-all" && request.method() === "POST") return json(route, { status: "ok", revoked_count: 2 });
    if (path === "/api/v1/school-assistant/capabilities" && request.method() === "GET") return json(route, { mode: "school_business", model_required: false, intents: [] });
    if (path === "/api/v1/onboarding/readiness" && request.method() === "GET") return json(route, {
      scope: role === "platform_admin" ? "platform" : "school", ready_for_use: onboardingReady,
      completed_count: onboardingReady ? 1 : 0, total_required: 1,
      checks: [{ key: "organization", state: onboardingReady ? "ready" : "action_required", severity: "blocking", title: "学校组织", action_code: "setup_organization", action_path: "/organization/setup" }]
    });

    if (path === "/api/v1/dashboard/summary") {
      if (options.dashboardMode === "empty") {
        return json(route, {
          scope: { tenant_id: "tenant-school", school_id: "school-1" },
          updated_at: "2026-08-02T07:06:00Z",
          organization_statistics: {
            active_student_count: 0,
            grade_count: 0,
            class_count: 0,
            teacher_count: 0,
            grader_count: 0,
            empty_class_count: 0,
            unassigned_teacher_count: 0
          },
          statistics: {
            active_exam_count: 0,
            collecting_exam_count: 0,
            pending_review_question_count: 0,
            pending_review_submission_count: 0,
            pending_arbitration_count: 0,
            pending_arbitration_submission_count: 0,
            failed_submission_count: 0,
            unmatched_submission_count: 0,
            quality_issue_submission_count: 0,
            finalized_exam_count: 0
          },
          blocking_issues: [],
          active_exams: [],
          recent_activities: [],
          warnings: []
        });
      }
      return json(route, {
        scope: { tenant_id: "tenant-school", school_id: "school-1" },
        updated_at: "2026-08-02T07:06:00Z",
        organization_statistics: {
          active_student_count: 2,
          grade_count: 1,
          class_count: 1,
          teacher_count: 1,
          grader_count: 1,
          empty_class_count: 0,
          unassigned_teacher_count: 0
        },
        statistics: {
          active_exam_count: 3,
          collecting_exam_count: 1,
          pending_review_question_count: 107,
          pending_review_submission_count: 6,
          pending_arbitration_count: 3,
          pending_arbitration_submission_count: 2,
          failed_submission_count: 1,
          unmatched_submission_count: 1,
          quality_issue_submission_count: 1,
          finalized_exam_count: 1
        },
        blocking_issues: [
          { code: "failed_submissions", label: "答题卡处理失败", count: 1, unit: "份", impact: "会阻断后续阅卷", action: "查看并重试", drilldown_path: "/capture?issue=failed" },
          { code: "unmatched_submissions", label: "答题卡未匹配学生", count: 1, unit: "份", impact: "无法计入学生成绩", action: "确认学生身份", drilldown_path: "/capture?issue=unmatched" },
          { code: "pending_arbitration", label: "待人工复核", count: 2, unit: "份", impact: "未确认最终得分", action: "开始复核", drilldown_path: "/arbitration?status=pending" }
        ],
        active_exams: [
          {
            id: "exam-1",
            name: "2026 春季数学期中考试",
            subject: "math",
            status: "collecting",
            submission_count: 48,
            failed_count: 1,
            quality_issue_count: 1,
            unmatched_count: 0,
            created_at: "2026-08-01T00:00:00Z"
          },
          {
            id: "exam-2",
            name: "高二语文月考",
            subject: "chinese",
            status: "draft",
            submission_count: 0,
            failed_count: 0,
            quality_issue_count: 0,
            unmatched_count: 0,
            created_at: "2026-07-31T00:00:00Z"
          },
          {
            id: "exam-3",
            name: "高一英语期末考试",
            subject: "english",
            status: "grading",
            submission_count: 126,
            failed_count: 0,
            quality_issue_count: 0,
            unmatched_count: 0,
            created_at: "2026-07-30T00:00:00Z"
          }
        ],
        recent_activities: [],
        warnings: []
      });
    }

    if (path === "/api/v1/schools") {
      return json(route, { schools: [{ id: "school-1", tenant_id: "tenant-school", name: "示范学校", code: "DEMO", status: "active" }] });
    }
    if (path === "/api/v1/grades") return json(route, { grades: [{ id: "grade-1", tenant_id: "tenant-school", school_id: "school-1", name: "高二", code: "G11", academic_year: "2026-2027", level_no: 11, education_stage: "senior", status: "active" }] });
    if (path === "/api/v1/classes") return json(route, { classes: [{ id: "class-1", school_id: "school-1", grade_id: "grade-1", name: "高二（1）班", code: "G11-01", status: "active" }] });
    if (path === "/api/v1/exam-templates") return json(route, { exam_templates: [{
      id: "00000000-0000-0000-0000-000000000601",
      code: "system.senior.standard",
      name: "系统通用高中考试方案",
      description: "适合校内期中、期末和阶段考试，可在本场考试中继续修改。",
      education_stage: "senior",
      version: 1,
      source: "system",
      recommended: true,
      subjects: [
        { id: "template-chinese", subject: "chinese", total_score: 150, duration_minutes: 150, candidate_rule: "all_selected_classes", sort_order: 1, sections: [{ id: "template-chinese-all", title: "全卷", question_type: "short_answer", question_count: 15, score_per_question: 10, sort_order: 1 }] },
        { id: "template-math", subject: "math", total_score: 150, duration_minutes: 120, candidate_rule: "all_selected_classes", sort_order: 2, sections: [{ id: "template-math-all", title: "全卷", question_type: "calculation", question_count: 15, score_per_question: 10, sort_order: 1 }] },
        { id: "template-english", subject: "english", total_score: 150, duration_minutes: 120, candidate_rule: "all_selected_classes", sort_order: 3, sections: [{ id: "template-english-all", title: "全卷", question_type: "short_answer", question_count: 15, score_per_question: 10, sort_order: 1 }] }
      ]
    }] });
    if (path === "/api/v1/students") return json(route, { students: [{ id: "student-1", school_id: "school-1", class_id: "class-1", name: "陈同学", student_no: "S001", status: "active" }, { id: "student-2", school_id: "school-1", class_id: "class-1", name: "林同学", student_no: "S002", status: "active" }], has_more: false });
    if (path === "/api/v1/users") return json(route, { users: [{ id: "user-school_admin", username: "school_admin", display_name: "学校管理员", status: "active", roles: ["school_admin"] }, { id: "grader-1", username: "math_grader", display_name: "数学阅卷老师", status: "active", roles: ["grader"] }], has_more: false });
	if (path === "/api/v1/roles") return json(route, { roles: [{ code: "teacher", name: "教师", scope_type: "class" }, { code: "grader", name: "阅卷员", scope_type: "exam_task" }, { code: "arbitrator", name: "仲裁员", scope_type: "exam_task" }] });
    if (path === "/api/v1/ocr/availability") {
      return json(route, {
        generated_at: "2026-08-02T07:06:00Z",
        worker: {
          status: "ok",
          availability: "online",
          automation_available: true,
          fresh_instances: 1,
          stale_instances: 0,
          total_instances: 1
        }
      });
    }

    if (path === "/api/v1/exam-management" && request.method() === "GET") return json(route, { exams: createdManagementExam ? [managementExam, createdManagementExam] : [managementExam], has_more: false });
    const managementCommandMatch = path.match(/^\/api\/v1\/exam-management\/commands\/([^/]+)$/);
    if (managementCommandMatch) {
      const commandId = decodeURIComponent(managementCommandMatch[1]);
      const receipt = managementReceipts.get(commandId);
      return json(route, { command: { command_id: commandId, status: receipt ? "succeeded" : "not_accepted", ...(receipt ? { exam: receipt.exam } : {}) } });
    }
    const managementMatch = path.match(/^\/api\/v1\/exam-management\/(session|standalone)\/([^/]+)(?:\/(archive-check|archive|restore))?$/);
    const selectedManagementExam = managementMatch ? [managementExam, createdManagementExam].find((exam) => exam && exam.kind === managementMatch[1] && exam.id === decodeURIComponent(managementMatch[2])) : null;
    if (managementMatch && selectedManagementExam) {
      const action = managementMatch[3];
      if (action === "archive-check") return json(route, { check: { allowed: selectedManagementExam.can_archive, blockers: selectedManagementExam.archive_blockers ?? [] } });
      if (action === "archive" || action === "restore") {
        const input = request.postDataJSON() as { command_id: string };
        const requestKey = `${path}:${request.postData()}`;
        const receipt = managementReceipts.get(input.command_id);
        if (receipt) return receipt.request === requestKey ? json(route, { exam: receipt.exam }) : json(route, { error: { code: "idempotency_key_reused_with_different_request" } }, 409);
        if (action === "archive" ? !selectedManagementExam.can_archive : !selectedManagementExam.can_restore) return json(route, { error: { code: "exam_archive_blocked", message: "模拟考试尚不满足操作条件" } }, 409);
        if (action === "archive") selectedManagementExam.archived_at = "2026-10-04T00:00:00Z";
        else delete selectedManagementExam.archived_at;
        selectedManagementExam.can_edit = action === "restore";
        selectedManagementExam.can_archive = action === "restore";
        selectedManagementExam.can_restore = action === "archive";
        selectedManagementExam.revision += 1;
        managementReceipts.set(input.command_id, { request: requestKey, exam: structuredClone(selectedManagementExam) });
      }
      if (!action && request.method() === "PATCH") {
        const input = request.postDataJSON() as { name?: string; exam_type?: string; class_ids?: string[] };
        if (input.name) selectedManagementExam.name = input.name;
        if (input.exam_type) selectedManagementExam.exam_type = input.exam_type;
        if (input.class_ids) selectedManagementExam.default_class_ids = input.class_ids;
        selectedManagementExam.revision += 1;
      }
      return json(route, { exam: selectedManagementExam });
    }

    if (path === "/api/v1/exams") {
      if (route.request().method() === "POST") {
        return json(route, { exam: { id: "exam-created", school_id: "school-1", name: "2026-2027学年高二期中考试", subject: "math", exam_type: "midterm_exam", total_score: 150, status: "draft", grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "after_admin_approval", class_ids: ["class-1"] } });
      }
      return json(route, {
        exams: [{
          id: "exam-1",
          tenant_id: "tenant-school",
          school_id: "school-1",
          name: "2026 春季数学期中考试",
          subject: "math",
          exam_type: "midterm",
          total_score: 150,
          status: "collecting",
          grading_mode: "ai_assisted",
          appeal_enabled: true,
          publish_policy: "manual",
          created_by: "user-school_admin",
          class_ids: [],
          created_at: "2026-08-01T00:00:00Z"
        }]
      });
    }
    const examDetailMatch = path.match(/^\/api\/v1\/exams\/([^/]+)$/);
    if (examDetailMatch && request.method() === "GET") {
      const examID = decodeURIComponent(examDetailMatch[1]);
      const newlyCreated = examID.startsWith("exam-created");
      const physics = examID === "exam-physics";
      return json(route, { exam: {
        id: examID,
        tenant_id: "tenant-school",
        school_id: "school-1",
        name: newlyCreated ? "2026-2027学年高二期中考试 · 数学" : physics ? "2026 春季物理期中考试" : "2026 春季数学期中考试",
        subject: physics ? "physics" : "math",
        exam_type: "midterm",
        total_score: physics ? 100 : 150,
        status: newlyCreated ? "draft" : "collecting",
        grading_mode: "ai_assisted",
        appeal_enabled: true,
        publish_policy: "manual",
        created_by: "user-school_admin",
        class_ids: [],
        revision: 3,
        created_at: "2026-08-01T00:00:00Z"
      } });
    }
    if (path === "/api/v1/exam-sessions" && route.request().method() === "POST") {
      createdManagementExam = {
        ...managementExam, id: "session-created", kind: "session", name: "2026-2027学年高二期中考试", revision: 1,
        exams: [{ ...managementExam.exams[0], id: "exam-created-math", subject: "math", status: "draft", name: "2026-2027学年高二期中考试 · 数学", exam_session_id: "session-created", exam_session_name: "2026-2027学年高二期中考试", revision: 1 }],
        inherited_subject_ids: ["exam-created-math"], can_archive: true, archive_blockers: [],
        progress: { status: "draft", label: "草稿", total_count: 1, ready_count: 0, published_count: 0, counts: { draft: 1 }, facts_available: true }
      };
      return json(route, {
        exam_session: {
          id: "session-created",
          school_id: "school-1",
          grade_id: "grade-1",
          name: "2026-2027学年高二期中考试",
          exam_type: "midterm_exam",
          status: "draft",
          exams: [{
            id: "exam-created-math",
            tenant_id: "tenant-school",
            school_id: "school-1",
            name: "2026-2027学年高二期中考试 · 数学",
            subject: "math",
            exam_type: "midterm_exam",
            total_score: 150,
            status: "draft",
            grading_mode: "ai_assisted",
            appeal_enabled: true,
            publish_policy: "after_admin_approval",
            created_by: "user-school_admin",
            class_ids: ["class-1"],
            revision: 1,
            created_at: "2026-08-02T07:06:00Z"
          }]
        }
      });
    }
    if (path === "/api/v1/review-tasks" && request.method() === "GET") {
      return json(route, { tasks: [], next_cursor: "", has_more: false });
    }
    if (/^\/api\/v1\/exams\/[^/]+\/scoring-readiness$/.test(path) && request.method() === "GET") {
      return json(route, { scoring_readiness: { ready: true, checks: [], metrics: {} } });
    }
    if (/^\/api\/v1\/exams\/[^/]+\/scoring-summary$/.test(path) && request.method() === "GET") {
      return json(route, { scoring_summary: { questions: [] } });
    }
    if (/^\/api\/v1\/exams\/[^/]+\/readiness$/.test(path) && request.method() === "GET") {
      return json(route, { readiness: {
        ready: false,
        confirmed: false,
        configuration_hash: "readiness-demo",
        import_snapshot_available: false,
        checks: [
          { code: "students", label: "学生范围", passed: true, severity: "blocker", message: "已选择 1 个班级", section: "students" },
          { code: "paper", label: "上传试卷", passed: false, severity: "blocker", message: "请上传并确认考试资料", section: "paper" },
          { code: "questions", label: "题目与答案", passed: false, severity: "blocker", message: "请完成题目和标准答案配置", section: "questions" },
          { code: "template", label: "答题卡模板", passed: false, severity: "blocker", message: "请创建并锁定答题卡模板", section: "template" }
        ]
      } });
    }

    const workspaceMatch = path.match(/^\/api\/v1\/exams\/([^/]+)\/workspace$/);
    if (workspaceMatch && request.method() === "GET") {
      const examID = decodeURIComponent(workspaceMatch[1]);
      const newlyCreated = examID.startsWith("exam-created");
      const subject = examID === "exam-physics" ? "physics" : "math";
      return json(route, {
        workspace: {
          exam_id: examID,
          read_only: examID.startsWith("exam-created") ? Boolean(createdManagementExam?.archived_at) : Boolean(managementExam.archived_at),
          ...(newlyCreated ? { exam_session_id: "session-created" } : options.sessionSubjects ? { exam_session_id: "session-1" } : {}),
          exam_name: newlyCreated ? "2026-2027学年高二期中考试 · 数学" : "2026 春季数学期中考试",
          ...(options.sessionSubjects ? { exam_session_name: "2026 春季期中考试" } : {}),
          subject_exams: options.sessionSubjects ? [
            { exam_id: "exam-1", subject: "math", total_score: 150 },
            { exam_id: "exam-physics", subject: "physics", total_score: 100 }
          ] : [{ exam_id: examID, subject, total_score: 150 }],
          exam_status: newlyCreated ? "draft" : "collecting",
          revision: 3,
          stage: newlyCreated ? "prepare" : "capture",
          stages: [
            { key: "prepare", label: "开考准备", state: newlyCreated ? "current" : "completed", action_route: `/exams/${examID}/settings` },
            { key: "capture", label: "答卷导入", state: newlyCreated ? "pending" : "current", action_route: `/exams/${examID}/capture` },
            { key: "grading", label: "阅卷", state: "pending", action_route: `/exams/${examID}/grading` },
            { key: "quality", label: "复核与异常", state: "pending", action_route: `/exams/${examID}/quality` },
            { key: "results", label: "成绩与报告", state: "pending", action_route: `/exams/${examID}/scores` }
          ],
          stage_progress: [
            { stage: "prepare", status: "completed", completed: 5, total: 5, unit: "项", summary: "已通过 5 / 5 项开考检查" },
            { stage: "capture", status: "current", completed: 6, unit: "份", summary: "已导入 6 份答卷" },
            { stage: "grading", status: "pending", completed: 0, total: 107, unit: "个任务", summary: "尚未开始阅卷" },
            { stage: "quality", status: "pending", completed: 0, total: 3, unit: "个任务", summary: "尚未开始人工复核" },
            { stage: "results", status: "pending", summary: "成绩尚未发布" }
          ],
          blockers: [{
            code: "failed_submissions",
            title: "答卷处理失败",
            message: "1 份答卷处理失败，会阻断后续阅卷",
            severity: "blocker",
            action_label: "查看并重试",
            action_route: `/exams/${examID}/capture`
          }],
          warnings: [{
            code: "quality_issues",
            title: "图像质量需要关注",
            message: "1 份答卷存在图像质量问题",
            severity: "warning",
            action_label: "查看异常",
            action_route: `/exams/${examID}/capture`
          }],
          counts: {
            paper_count: 1,
            question_count: newlyCreated ? 0 : 19,
            submission_count: 6,
            failed_submission_count: 1,
            quality_issue_submission_count: 1,
            unmatched_submission_count: 0,
            pending_review_count: 107,
            pending_arbitration_count: 3
          },
          next_actions: [{
            code: "failed_submissions",
            label: "查看并重试",
            description: "答卷处理失败",
            route: `/exams/${examID}/capture`,
            priority: "high"
          }],
          risk_tier: "R2",
          subject_summary: {
            code: subject,
            label: subject === "physics" ? "物理" : "数学",
            total_score: subject === "physics" ? 100 : 150,
            question_count: 19,
            configured_question_count: newlyCreated ? 0 : 19,
            frozen_question_count: newlyCreated ? 0 : 19,
            risk_tier_source: "snapshot",
            question_types: { single_choice: 10, extended_response: 9 }
          },
          updated_at: "2026-08-12T08:00:00Z"
        }
      });
    }
    if (path === "/api/v1/exams/exam-1/capture-batches" && request.method() === "GET") {
      return json(route, { batches: [], next_cursor: "", has_more: false });
    }
    if (/^\/api\/v1\/exams\/[^/]+\/submissions$/.test(path)) return json(route, { submissions: [] });
    if (path === "/api/v1/ocr/tasks") return json(route, { tasks: [] });

    return json(route, { error: { code: "e2e_mock_missing", message: `未配置测试接口 ${path}` } }, 404);
  });
}

export function captureFailedRequests(page: Page) {
  const failures: string[] = [];
  page.on("requestfailed", (request) => {
    failures.push(`${request.method()} ${request.url()} ${request.failure()?.errorText ?? ""}`);
  });
  return failures;
}
