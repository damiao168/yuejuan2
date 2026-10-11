import { Button, Result } from "antd";
import { ArrowRight } from "lucide-react";
import type { AppRoute } from "../router/routes";
import { routePresentation, routes } from "../router/routes";
import type { ProductExperience } from "../router/experience";

export function ModulePage({
  route,
  experience,
  onNavigate
}: Readonly<{
  route: AppRoute;
  experience: ProductExperience;
  onNavigate?: (path: string) => void;
}>) {
  const presentation = routePresentation(route, experience);
  const replacement = route.replacementPath
    ? routes.find((candidate) => candidate.path === route.replacementPath)
    : undefined;
  const replacementTitle = replacement ? routePresentation(replacement, experience).title : undefined;

  return (
    <div className="page-stack">
      <section className="page-heading">
        <div>
          <h1>{presentation.title}</h1>
          <p>该功能正在开发中，暂未开放使用。</p>
        </div>
      </section>
      <Result
        status="info"
        title="功能建设中"
        subTitle={`「${presentation.title}」正在开发，当前版本暂未开放。`}
        extra={
          replacement && replacementTitle && onNavigate ? (
            <Button type="primary" icon={<ArrowRight size={16} />} onClick={() => onNavigate(route.replacementPath!)}>
              前往{replacementTitle}
            </Button>
          ) : undefined
        }
      />
    </div>
  );
}
