import { Alert, AlertDescription, AlertTitle } from "@components/Alert";
import { IconFlask } from "@tabler/icons-react";
import { useT } from "@i18n/index";

export function BetaNotice() {
  const t = useT();
  return (
    <Alert variant="warning" className="my-4">
      <IconFlask className="h-4 w-4" />
      <AlertTitle>{t("Error reporting is in beta")}</AlertTitle>
      <AlertDescription className="text-muted-foreground">
        <p>
          {t("SDK support is still rolling out. Want it in your SDK?")}{" "}
          <a target="_blank" className="underline hover:text-foreground" href="https://github.com/aptabase">
            {t("We welcome contributions on GitHub")}
          </a>
          .
        </p>
      </AlertDescription>
    </Alert>
  );
}
