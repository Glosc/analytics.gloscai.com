import { Page, PageHeading } from "@components/Page";
import { useCurrentApp } from "@features/apps";
import { Navigate } from "react-router-dom";
import { BetaNotice } from "./errors/BetaNotice";
import { ErrorsList } from "./errors/ErrorsList";
import { useT } from "@i18n/index";

Component.displayName = "ErrorsPage";
export function Component() {
  const t = useT();
  const app = useCurrentApp();

  if (!app) return <Navigate to="/" />;

  return (
    <Page title={t("Error Logs")}>
      <div className="flex justify-between items-center">
        <PageHeading
          title={t("Errors")}
          subtitle={t("Track and debug crashes and errors in your app")}
        />
      </div>
      <BetaNotice />
      <ErrorsList appId={app.id} />
    </Page>
  );
}
