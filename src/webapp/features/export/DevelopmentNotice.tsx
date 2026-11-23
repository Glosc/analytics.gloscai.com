import { Alert, AlertTitle, AlertDescription } from "@components/Alert";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useT } from "@i18n/index";

export function DevelopmentNotice() {
  const t = useT();
  return (
    <Alert variant="warning" className="max-w-xl mx-auto">
      <IconAlertTriangle className="h-4 w-4" />
      <AlertTitle>{t("Experimental")}</AlertTitle>
      <AlertDescription className="text-muted-foreground">
        <p>
          {t("This feature is experimental and subject to change. We're working on better formats for larger amount of data.")}
        </p>
      </AlertDescription>
    </Alert>
  );
}
