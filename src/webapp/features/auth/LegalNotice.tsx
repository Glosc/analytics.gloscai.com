import { isManagedCloud } from "@features/env";
import { useT } from "@i18n/index";

type Props = {
  operation: "signin" | "signup";
};

export function LegalNotice(props: Props) {
  const t = useT();

  if (!isManagedCloud) return null;

  return (
    <p className="text-center mt-2 text-xs tracking-normal text-muted-foreground">
      {t(props.operation === "signin" ? "By signing in, you agree to our" : "By signing up, you agree to our")}{" "}
      <a
        href="https://aptabase.com/legal/terms"
        target="_blank"
        className="text-primary hover:underline"
      >
        {t("Terms of Service")}
      </a>{" "}
      {t("and")}{" "}
      <a
        href="https://aptabase.com/legal/privacy"
        target="_blank"
        className="text-primary hover:underline"
      >
        {t("Privacy Policy")}
      </a>
      .
    </p>
  );
}
