import { useCurrentApp } from "@features/apps";
import { isSupportEnabled } from "@features/env";
import { SupportNavCategory } from "@features/support";
import {
  IconActivityHeartbeat,
  IconAlertTriangle,
  IconCloudDownload,
  IconCode,
  IconGraph,
  IconLayoutGrid,
  IconSettings,
  IconUsers,
} from "@tabler/icons-react";
import { useLocation } from "react-router-dom";
import { NavCategory } from "./NavCategory";
import { NavItem } from "./NavItem";
import { useT } from "@i18n/index";

export function NavMenu(props: { onNavigation?: VoidFunction }) {
  const t = useT();
  const currentApp = useCurrentApp();
  const location = useLocation();

  return (
    <div className="flex flex-col h-full justify-between">
      <div className="space-y-6">
        <NavCategory>
          <NavItem label={t("Home")} href="/" icon={IconLayoutGrid} onNavigation={props.onNavigation} />
        </NavCategory>
        <NavCategory title={t("Application")}>
          <NavItem
            label={t("Dashboard")}
            disabled={!currentApp}
            href={`/${currentApp?.id}/`}
            icon={IconGraph}
            onNavigation={props.onNavigation}
          />
          <NavItem
            label={t("Live View")}
            disabled={!currentApp || !!currentApp.lockReason}
            href={`/${currentApp?.id}/live`}
            icon={IconActivityHeartbeat}
            onNavigation={props.onNavigation}
          />
          <NavItem
            label={t("User Sessions")}
            disabled={!currentApp || !!currentApp.lockReason}
            href={`/${currentApp?.id}/sessions${location.search}`}
            icon={IconUsers}
            onNavigation={props.onNavigation}
          />
          <NavItem
            label={t("Errors")}
            disabled={!currentApp || !!currentApp.lockReason}
            href={`/${currentApp?.id}/errors`}
            icon={IconAlertTriangle}
            onNavigation={props.onNavigation}
          />
          <NavItem
            label={t("Export")}
            disabled={!currentApp || !!currentApp.lockReason}
            href={`/${currentApp?.id}/export`}
            icon={IconCloudDownload}
            onNavigation={props.onNavigation}
          />
          <NavItem
            label={t("Instructions")}
            disabled={!currentApp}
            href={`/${currentApp?.id}/instructions`}
            icon={IconCode}
            onNavigation={props.onNavigation}
          />
          <NavItem
            label={t("Settings")}
            disabled={!currentApp || !currentApp.hasOwnership}
            disabledReason={
              currentApp && !currentApp.hasOwnership ? t("Settings are available only to application owners") : undefined
            }
            href={`/${currentApp?.id}/settings`}
            icon={IconSettings}
            onNavigation={props.onNavigation}
          />
        </NavCategory>
        {isSupportEnabled && <SupportNavCategory />}
      </div>
    </div>
  );
}
