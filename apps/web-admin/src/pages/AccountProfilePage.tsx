import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button } from "antd";
import { RefreshCw } from "lucide-react";
import { getAccountProfile, type AccountProfile } from "../api/auth";
import { getUserErrorMessage } from "../api/client";
import { hasEveryPermission, type SessionUser } from "../auth/session";
import { ErrorState, LoadingState } from "../components/PageState";
import { isSubjectManagedTeacher, teachingSubjectsLabel } from "../constants/teachingSubjects";
import { passwordMinimumForRoles } from "../auth/loginSecurity";
import { SessionManagementPage } from "./SessionManagementPage";
import "./account-profile.css";

export function AccountProfilePage({ currentUser, onNavigate, onLoggedOut }: Readonly<{
  currentUser: SessionUser;
  onNavigate: (path: string) => void;
  onLoggedOut: () => void;
}>) {
  const [profile, setProfile] = useState<AccountProfile>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [securityRefresh, setSecurityRefresh] = useState(0);
  const requestVersion = useRef(0);
  const load = useCallback(async () => {
    const version = ++requestVersion.current;
    setLoading(true);
    setError("");
    try {
      const result = await getAccountProfile();
      // 路由已按账号换 key；离页或后发刷新时，旧请求不能覆盖当前资料。
      if (version === requestVersion.current) setProfile(result.profile);
    } catch (reason) {
      if (version === requestVersion.current) setError(getUserErrorMessage(reason, "个人信息暂时无法读取，请稍后重试。"));
    } finally {
      if (version === requestVersion.current) setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); return () => { requestVersion.current++; }; }, [load]);
  const subjectManaged = isSubjectManagedTeacher(currentUser.roles);
  const canManageTeachers = currentUser.roles.some(role => ["school_admin", "tenant_admin"].includes(role))
    && !currentUser.roles.includes("platform_admin") && hasEveryPermission(currentUser, ["org:manage"]);
  const administrator = currentUser.roles.some(role => ["school_admin", "tenant_admin", "platform_admin"].includes(role));

  return <div className="account-profile-page">
    <header className="account-profile-heading"><div><h1>个人信息与账户安全</h1><p>{subjectManaged ? "查看个人资料和任教学科，管理密码与登录设备。" : "查看个人资料，管理密码与登录设备。"}</p></div>
      <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => { setSecurityRefresh(version => version + 1); void load(); }}>刷新</Button>
    </header>
    {error ? <ErrorState message={error} onRetry={() => void load()} /> : null}
    {loading && !profile ? <LoadingState label="正在读取个人信息" /> : null}
    {profile ? <>
      <section className="account-profile-section" aria-label="本人基本资料">
        <h2>基本资料</h2><dl>
          <div><dt>姓名</dt><dd>{profile.display_name || "未填写"}</dd></div>
          <div><dt>用户名</dt><dd>{profile.username}</dd></div>
          {subjectManaged || administrator || profile.employee_no ? <div><dt>教职工号</dt><dd>{profile.employee_no || "未填写"}</dd></div> : null}
          <div><dt>手机号</dt><dd>{profile.phone_masked || "未填写"}</dd></div>
          <div><dt>所属学校</dt><dd>{currentUser.school}</dd></div>
        </dl>
        <p className="muted">{administrator ? "姓名、手机号和所属学校如需变更，请联系机构管理员。" : "姓名、手机号等资料如需变更，请联系学校管理员。"}</p>
      </section>
      {subjectManaged ? <section className="account-profile-section" aria-label="本人任教学科">
        <h2>任教学科</h2><p className="account-profile-subjects">{teachingSubjectsLabel(profile.teaching_subjects)}</p>
        <Alert showIcon type={profile.teaching_subjects.length ? "info" : "warning"}
          message={profile.teaching_subjects.length ? "学校管理员按考试学科分配对应教师的阅卷任务。" : "尚未设置任教学科，请联系学校管理员补充后再分配阅卷任务。"}
          description="任教学科由学校管理员在“人员与权限”中维护；如需调整，请联系管理员。" />
      </section> : null}
      {canManageTeachers ? <div className="account-profile-actions"><Button type="primary" onClick={() => onNavigate("/members/teachers")}>管理阅卷教师学科</Button></div> : null}
    </> : null}
    <SessionManagementPage embedded refreshVersion={securityRefresh} onLoggedOut={onLoggedOut} minimumPasswordLength={passwordMinimumForRoles(currentUser.roles)} />
  </div>;
}
