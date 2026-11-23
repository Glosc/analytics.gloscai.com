import { login, useOAuthProviders } from "@features/auth";
import { Page } from "@components/Page";
import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { DataResidency } from "./DataResidency";
import { LegalNotice } from "./LegalNotice";
import { RegionSwitch } from "./RegionSwitch";
import { SignInWithGitHub } from "./SignInWithGitHub";
import { SignInWithGoogle } from "./SignInWithGoogle";
import { Logo } from "./Logo";
import { Button } from "@components/Button";
import { TextInput } from "@components/TextInput";
import { LanguageSwitch } from "@components/LanguageSwitch";
import { useT } from "@i18n/index";

type FormStatus = "idle" | "loading";

type StatusMessageProps = {
  status: FormStatus;
  error: string | null;
};

const StatusMessage = (props: StatusMessageProps) => {
  const t = useT();

  if (props.error) {
    return <span className="text-destructive">{props.error}</span>;
  }

  return (
    <span className="block">
      {t("Don't have an account?")}{" "}
      <Link className="font-semibold text-foreground" to="/auth/register">
        {t("Sign up for free.")}
      </Link>
    </span>
  );
};

const OAuthErrorMessage = () => {
  const t = useT();
  const [params] = useSearchParams();

  if (params.get("error") !== "oauth_failed") {
    return null;
  }

  return (
    <p className="mx-auto text-center mb-10 text-destructive text-sm">
      {t("Sign in failed. Please try again.")}
    </p>
  );
};

Component.displayName = "LoginPage";
export function Component() {
  const t = useT();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [status, setStatus] = useState<FormStatus>("idle");
  const [error, setError] = useState<string | null>(null);

  const providers = useOAuthProviders();
  const showOAuth = providers.github || providers.google;

  const handleSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setStatus("loading");
    setError(null);

    let result;
    try {
      result = await login(username, password);
    } catch {
      setStatus("idle");
      return;
    }

    if (!result.ok) {
      setError(
        result.reason === "rate_limited"
          ? t("Too many sign-in attempts. Please wait a few minutes and try again.")
          : result.reason === "invalid_credentials"
            ? t("Incorrect username or password.")
            : t("Something went wrong. Please try again.")
      );
      setStatus("idle");
      return;
    }

    location.href = "/";
  };

  return (
    <Page title={t("Login")}>
      <div className="mx-auto text-center mb-10">
        <OAuthErrorMessage />
      </div>

      <div className="sm:mx-auto sm:w-full sm:max-w-md text-center">
        <Logo className="mx-auto h-12 w-auto text-primary" />
        <h2 className="text-center text-3xl text-foreground font-bold">{t("Sign in to your account")}</h2>
        <DataResidency />
      </div>
      <div className="mt-8 sm:mx-auto sm:w-full sm:max-w-md">
        <div className="py-8 px-4 sm:rounded-lg sm:px-10">
          {showOAuth && (
            <>
              <div className="space-y-2">
                {providers.github && <SignInWithGitHub />}
                {providers.google && <SignInWithGoogle />}
              </div>

              <div className="relative my-4">
                <div className="absolute inset-0 flex items-center" aria-hidden="true">
                  <div className="w-full border-t" />
                </div>
                <div className="relative flex justify-center text-sm">
                  <span className="px-2 bg-muted">{t("OR")}</span>
                </div>
              </div>
            </>
          )}

          <form onSubmit={handleSubmit} className="flex flex-col space-y-4">
            <TextInput
              label={t("Username")}
              name="username"
              type="text"
              placeholder="peter.parker"
              autoComplete="username"
              value={username}
              required={true}
              onChange={(e) => setUsername(e.target.value)}
            />
            <TextInput
              label={t("Password")}
              name="password"
              type="password"
              placeholder="••••••••"
              autoComplete="current-password"
              value={password}
              required={true}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Button loading={status === "loading"}>{t("Sign in")}</Button>
            <p className="text-center text-sm h-10 text-muted-foreground">
              <StatusMessage status={status} error={error} />
            </p>
          </form>
        </div>
        <LegalNotice operation="signin" />
        <RegionSwitch />
        <div className="mt-4 flex justify-center">
          <LanguageSwitch />
        </div>
      </div>
    </Page>
  );
}
