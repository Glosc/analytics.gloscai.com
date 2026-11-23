import { Page, PageHeading } from "@components/Page";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@components/Tabs";
import { useCurrentApp } from "@features/apps";
import { Navigate } from "react-router-dom";
import { AppSharing } from "./AppSharing";
import { DangerZone } from "./DangerZone";
import { GeneralSettings } from "./GeneralSettings";
import { OwnershipTransfer } from "./OwnershipTransfer";
import { useT } from "@i18n/index";

Component.displayName = "SettingsPage";
export function Component() {
  const t = useT();
  const app = useCurrentApp();

  if (!app || !app.hasOwnership) return <Navigate to="/" />;

  return (
    <Page title={`${app.name} - ${t("Settings")}`}>
      <PageHeading title={t("Settings")} subtitle={t("Manage your app settings")} />

      <Tabs defaultValue="general" className="mt-8">
        <TabsList>
          <TabsTrigger value="general">{t("General")}</TabsTrigger>
          <TabsTrigger value="sharing">{t("Sharing")}</TabsTrigger>
          <TabsTrigger value="ownership">{t("Ownership Transfer")}</TabsTrigger>
          <TabsTrigger value="danger">{t("Danger Zone")}</TabsTrigger>
        </TabsList>
        <TabsContent value="general">
          <GeneralSettings app={app} />
        </TabsContent>
        <TabsContent value="sharing">
          <AppSharing app={app} />
        </TabsContent>
        <TabsContent value="ownership">
          <OwnershipTransfer app={app} />
        </TabsContent>
        <TabsContent value="danger">
          <DangerZone app={app} />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
