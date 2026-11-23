import { Link } from "react-router-dom";
import { useBilling } from "./useBilling";
import { Alert, AlertDescription } from "@components/Alert";
import { isBillingEnabled } from "@features/env";
import { useT } from "@i18n/index";

export function TrialReminder() {
  const t = useT();
  const { data: billing } = useBilling();

  if (!isBillingEnabled || !billing || !billing.plan.freeTrialEndsAt) return null;

  const endsAt = new Date(billing.plan.freeTrialEndsAt);
  const differenceInMs = new Date(endsAt).getTime() - new Date().getTime();
  const days = Math.floor(differenceInMs / (1000 * 60 * 60 * 24));

  if (days <= -1) return null;

  return (
    <Link to="/billing">
      <Alert variant="warning" className="p-2">
        <AlertDescription className="text-sm">
          {days === 1
            ? t("Trial ends tomorrow")
            : days > 0
              ? t("Trial ends in {days} days", { days })
              : days === 0
                ? t("Trial ends today")
                : t("Trial expired")}
        </AlertDescription>
      </Alert>
    </Link>
  );
}
