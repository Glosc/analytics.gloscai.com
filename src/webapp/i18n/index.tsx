import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { enUS, zhCN, type Locale as DateFnsLocale } from "date-fns/locale";
import { zh } from "./zh";

export type Locale = "en" | "zh";

export type TranslationParams = Record<string, string | number>;

export const LOCALE_STORAGE_KEY = "locale";

function isLocale(value: string | null | undefined): value is Locale {
  return value === "en" || value === "zh";
}

function detectLocale(): Locale {
  try {
    const stored = localStorage.getItem(LOCALE_STORAGE_KEY);
    if (isLocale(stored)) return stored;
  } catch {
    // localStorage may be unavailable (private mode, tests, etc.)
  }

  const languages = navigator.languages?.length ? navigator.languages : [navigator.language || "en"];
  return languages.some((lang) => lang.toLowerCase().startsWith("zh")) ? "zh" : "en";
}

// Module-level copy so helpers that are not React components (date/number
// formatting) can follow the selected locale.
let activeLocale: Locale = detectLocale();

export function getLocale(): Locale {
  return activeLocale;
}

export function dateLocale(locale: Locale = activeLocale): DateFnsLocale {
  return locale === "zh" ? zhCN : enUS;
}

export function translate(locale: Locale, key: string, params?: TranslationParams): string {
  const template = (locale === "zh" ? zh[key] : undefined) ?? key;

  if (!params) return template;

  return template.replace(/\{(\w+)\}/g, (match, name: string) => {
    const value = params[name];
    return value === undefined ? match : String(value);
  });
}

export type I18nContextValue = {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: string, params?: TranslationParams) => string;
};

const I18nContext = createContext<I18nContextValue>({
  locale: activeLocale,
  setLocale: () => {},
  t: (key, params) => translate(activeLocale, key, params),
});

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(activeLocale);

  const setLocale = (next: Locale) => {
    activeLocale = next;
    try {
      localStorage.setItem(LOCALE_STORAGE_KEY, next);
    } catch {
      // ignore storage failures
    }
    setLocaleState(next);
  };

  useEffect(() => {
    activeLocale = locale;
    document.documentElement.lang = locale === "zh" ? "zh-CN" : "en";
  }, [locale]);

  const value = useMemo<I18nContextValue>(
    () => ({
      locale,
      setLocale,
      t: (key, params) => translate(locale, key, params),
    }),
    [locale]
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  return useContext(I18nContext);
}

export function useT() {
  return useI18n().t;
}
