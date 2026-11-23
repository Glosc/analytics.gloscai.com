import { api } from "@fns/api";
import { trackEvent } from "@aptabase/web";

export type UserAccount = {
  id: string;
  name: string;
  username: string;
  email: string;
  role: "admin" | "user" | string;
  avatarUrl: string;
};

export type OAuthProviders = {
  github: boolean;
  google: boolean;
};

export async function getOAuthProviders(): Promise<OAuthProviders> {
  return await api.get<OAuthProviders>("/_auth/providers");
}

export type LoginOutcome =
  | { ok: true; user: UserAccount }
  | { ok: false; reason: "invalid_credentials" | "rate_limited" | "failed" };

export async function login(username: string, password: string): Promise<LoginOutcome> {
  const [status, response] = await api.fetch("POST", "/_auth/login", {
    username,
    password,
  });

  if (status === 401) return { ok: false, reason: "invalid_credentials" };
  if (status === 429) return { ok: false, reason: "rate_limited" };
  if (status !== 200) {
    await api.handleError(status, response);
    return { ok: false, reason: "failed" };
  }

  return { ok: true, user: (await response.json()) as UserAccount };
}

export type RegisterOutcome = { ok: true } | { ok: false; reason: "username_taken" | "failed" };

export async function register(name: string, username: string, password: string): Promise<RegisterOutcome> {
  const [status, response] = await api.fetch("POST", "/_auth/register", {
    name,
    username,
    password,
  });

  if (status === 409) return { ok: false, reason: "username_taken" };
  if (status !== 200) {
    await api.handleError(status, response);
    return { ok: false, reason: "failed" };
  }

  trackEvent("register");
  return { ok: true };
}

export async function me(): Promise<UserAccount | null> {
  const [status, account] = await api.fetch("GET", "/_auth/me");

  if (status === 401) return null;

  return account.json() as Promise<UserAccount | null>;
}

export async function signOut(): Promise<void> {
  await api.fetch("POST", "/_auth/signout");
  location.href = "/auth";
}
