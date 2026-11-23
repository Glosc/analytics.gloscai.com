import { IconGift, IconMessageCircle } from "@tabler/icons-react";
import { NavCategory, NavItem } from "../navigation";
import { useSupport } from "./useSupport";
import { useT } from "@i18n/index";

export function SupportNavCategory() {
  const t = useT();
  const { toggleChat } = useSupport();

  return (
    <NavCategory title={t("Product")}>
      <NavItem label={t("Help & Feedback")} onClick={toggleChat} icon={IconMessageCircle} />
      <NavItem label={t("Affiliates")} href="https://aptabase.lemonsqueezy.com/affiliates" icon={IconGift} />
    </NavCategory>
  );
}
