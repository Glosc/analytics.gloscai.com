import { register, useOAuthProviders } from "@features/auth";
import { Page } from "@components/Page";
import { useState } from "react";
import { Link } from "react-router-dom";
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
  error: string | null;
};

const StatusMessage = (props: StatusMessageProps) => {
  const t = useT();

  if (props.error) {
    return <span className="text-destructive">{props.error}</span>;
  }

  return (
    <>
      {t("Already registered?")}{" "}
      <Link className="font-medium text-foreground" to="/auth">
        {t("Sign in")}
      </Link>{" "}
      {t("to your account.")}
    </>
  );
};

Component.displayName = "RegisterPage";
export function Component() {
  const t = useT();
  const [name, setName] = useState("");
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
      result = await register(name, username, password);
    } catch {
      setStatus("idle");
      return;
    }

    if (!result.ok) {
      setError(
        result.reason === "username_taken"
          ? t("That username is already taken.")
          : t("Something went wrong. Please try again.")
      );
      setStatus("idle");
      return;
    }

    location.href = "/";
  };

  return (
    <Page title={t("Sign up")}>
      <div className="sm:mx-auto sm:w-full sm:max-w-md">
        <Logo className="mx-auto h-12 w-auto text-primary" />
        <h2 className="text-center text-3xl font-bold">{t("Sign up for an account")}</h2>
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
              label={t("Name")}
              name="name"
              placeholder="Peter Parker"
              autoComplete="name"
              value={name}
              required={true}
              onChange={(e) => setName(e.target.value)}
            />
            <TextInput
              label={t("Username")}
              name="username"
              type="text"
              placeholder="peter.parker"
              autoComplete="username"
              description={t("3–40 characters: letters, numbers, dots, dashes and underscores.")}
              value={username}
              required={true}
              minLength={3}
              maxLength={40}
              onChange={(e) => setUsername(e.target.value)}
            />
            <TextInput
              label={t("Password")}
              name="password"
              type="password"
              placeholder="••••••••"
              autoComplete="new-password"
              description={t("At least 8 characters.")}
              value={password}
              required={true}
              minLength={8}
              maxLength={128}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Button loading={status === "loading"}>{t("Create account")}</Button>
            <p className="text-center text-sm h-10 text-muted-foreground">
              <StatusMessage error={error} />
            </p>
          </form>
        </div>
        <LegalNotice operation="signup" />
        <RegionSwitch />
        <div className="mt-4 flex justify-center">
          <LanguageSwitch />
        </div>
      </div>
    </Page>
  );
}
