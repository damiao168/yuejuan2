// @vitest-environment jsdom
import type { ComponentProps } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../auth/session";
import type { AppRoute } from "../router/routes";
import { navigationScrollClass, selectedNavigationPath, shellClassNames, ShellNavigation, ShellTopbar } from "./appLayoutPresentation";

const user: SessionUser = {
  id: "user-1", username: "teacher-1", displayName: "Teacher", name: "Teacher", role: "teacher", roles: ["teacher", "school_admin"],
  tenant: "tenant-1", school: "School", currentExam: "", permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: [], gradeIds: [], classIds: [] }
};
const topbar: ComponentProps<typeof ShellTopbar> = {
  mode: "standard", desktop: true, focusedContext: { title: "新建考试", backLabel: "返回考试列表", backPath: "/exams" },
  user, experience: "teacher", title: "任务", mock: false, accountMenu: { items: [] }, onNavigate: vi.fn(), onOpenNavigation: vi.fn()
};
const navigation: ComponentProps<typeof ShellNavigation> = {
  desktop: true, collapsed: false, scrollbarVisible: false, scrollbarFading: false,
  onShowScrollbar: vi.fn(), onHideScrollbar: vi.fn(), onCloseNavigation: vi.fn(), onToggleNavigation: vi.fn(),
  onNavigate: vi.fn(), onExperienceChange: vi.fn(), user, experience: "teacher", availableExperiences: ["admin", "teacher"],
  selectedPath: "/tasks", menuItems: [], accountMenu: { items: [] }
};

function markup(element: React.ReactNode) {
  const host = document.createElement("div");
  host.innerHTML = renderToStaticMarkup(element);
  return host;
}

describe("shell presentation boundaries", () => {
  it("selects the exam navigation section and maps exam creation back to the list", () => {
    const route = { key: "examWorkspace", path: "/workspace" } as AppRoute;
    expect(selectedNavigationPath(route)).toBe("/exams");
    expect(selectedNavigationPath(route, "scores")).toBe("/scores");
    expect(selectedNavigationPath({ ...route, key: "examCreate", path: "/exams/new" })).toBe("/exams");
    expect(selectedNavigationPath({ ...route, key: "dashboard", path: "/dashboard" })).toBe("/dashboard");
  });

  it("keeps immersive modes ahead of the exam workspace class", () => {
    expect(shellClassNames("grading", "examWorkspace")).toEqual({ frame: "app-frame immersive-frame", workspace: "workspace immersive-workspace" });
    expect(shellClassNames("focused", "examWorkspace")).toEqual({ frame: "app-frame focused-frame", workspace: "workspace focused-workspace" });
    expect(shellClassNames("standard", "examWorkspace")).toEqual({ frame: "app-frame", workspace: "workspace exam-workspace-shell" });
    expect(shellClassNames("standard", "dashboard")).toEqual({ frame: "app-frame", workspace: "workspace" });
  });

  it("always exposes the mobile scrollbar while desktop fade requires visibility", () => {
    expect(navigationScrollClass(false, false, true)).toBe("sidebar-main is-scrollbar-visible");
    expect(navigationScrollClass(true, false, true)).toBe("sidebar-main");
    expect(navigationScrollClass(true, true, false)).toBe("sidebar-main is-scrollbar-visible");
    expect(navigationScrollClass(true, true, true)).toBe("sidebar-main is-scrollbar-visible is-scrollbar-fading");
  });

  it("renders no standard desktop topbar but retains the mobile route and school context", () => {
    expect(markup(<ShellTopbar {...topbar} />).children).toHaveLength(0);
    const mobile = markup(<ShellTopbar {...topbar} desktop={false} mock={true} />);
    expect(mobile.querySelector(".mobile-shellbar")).not.toBeNull();
    expect(mobile.querySelector('[aria-label="打开主导航"]')).not.toBeNull();
    expect(mobile.textContent).toContain("School");
    expect(mobile.querySelector(".mock-badge")).not.toBeNull();
  });

  it("keeps focused account actions while grading only shows exit and environment on either screen size", () => {
    for (const desktop of [true, false]) {
      const focused = markup(<ShellTopbar {...topbar} desktop={desktop} mode="focused" mock={true} />);
      expect(focused.textContent).toContain("新建考试");
      expect(focused.querySelector('[aria-label="返回考试列表"]')).not.toBeNull();
      expect(focused.querySelector(".mock-badge")).toBeNull();
      expect(focused.querySelector(".user-button")?.textContent).toContain("teacher-1");
      const grading = markup(<ShellTopbar {...topbar} desktop={desktop} mode="grading" title="我的阅卷" mock={true} />);
      expect(grading.querySelector('[aria-label="退出阅卷"]')).not.toBeNull();
      expect(grading.textContent).not.toContain("阅卷端");
      expect(grading.textContent).not.toContain("我的阅卷");
      expect(grading.textContent).not.toContain("teacher-1");
      expect(grading.querySelector(".user-button")).toBeNull();
      expect(grading.querySelector(".topbar-actions")).toBeNull();
      expect(grading.querySelector(".mock-badge")).not.toBeNull();
    }
  });

  it("hides experience switching only for a collapsed desktop navigation", () => {
    expect(markup(<ShellNavigation {...navigation} />).querySelector(".experience-switcher")).not.toBeNull();
    const collapsed = markup(<ShellNavigation {...navigation} collapsed={true} />);
    expect(collapsed.querySelector(".experience-switcher")).toBeNull();
    expect(collapsed.querySelector('[aria-label="展开导航"]')?.getAttribute("aria-expanded")).toBe("false");
    const mobile = markup(<ShellNavigation {...navigation} desktop={false} collapsed={true} />);
    expect(mobile.querySelector(".experience-switcher")).not.toBeNull();
    expect(mobile.querySelector(".sidebar-toggle")).toBeNull();
  });

  it("uses the same username as the member list and keeps a valid name in the account description", () => {
    const named = markup(<ShellNavigation {...navigation} />);
    expect(named.querySelector(".sidebar-account-copy strong")?.textContent).toBe("teacher-1");
    expect(named.querySelector(".sidebar-account")?.getAttribute("aria-label")).toBe("账户菜单：teacher-1（Teacher）");
    const invalidName = markup(<ShellNavigation {...navigation} user={{ ...user, username: "demo_school_admin", displayName: "???????" }} />);
    expect(invalidName.querySelector(".sidebar-account-copy strong")?.textContent).toBe("demo_school_admin");
    expect(invalidName.querySelector(".sidebar-account")?.getAttribute("aria-label")).toBe("账户菜单：demo_school_admin");
    expect(invalidName.textContent).not.toContain("???????");
  });
});
