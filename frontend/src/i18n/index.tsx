import { Fragment, ReactNode } from 'react';
import { en, Messages } from './en';
import { fr } from './fr';

// Add a language: copy en.ts to <code>.ts, translate it, and register it here.
// TypeScript refuses to build if a translation is missing.
const catalogs = { en, fr } satisfies Record<string, Messages>;

export type Lang = keyof typeof catalogs;
export type Key = keyof Messages;
export const languages: Record<Lang, string> = { en: 'English', fr: 'Français' };

const STORAGE_KEY = 'kubepilot.lang';

function detect(): Lang {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved && saved in catalogs) return saved as Lang;
  } catch { /* storage disabled */ }
  for (const l of navigator.languages ?? [navigator.language]) {
    const base = l.toLowerCase().split('-')[0];
    if (base in catalogs) return base as Lang;
  }
  return 'en';
}

/** Current interface language (fixed for the page's lifetime: changing it reloads the page). */
export const lang: Lang = detect();
/** BCP 47 locale for dates and numbers. */
export const locale = lang === 'fr' ? 'fr-FR' : 'en-US';
document.documentElement.lang = lang;

export function setLang(l: Lang) {
  try { localStorage.setItem(STORAGE_KEY, l); } catch { /* storage disabled */ }
  location.reload();
}

type Vars = Record<string, string | number>;

/** Translates a key; `{name}` placeholders are replaced by `vars.name`. */
export function t(key: Key, vars?: Vars): string {
  const s: string = catalogs[lang][key] ?? en[key] ?? key;
  return vars ? s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m)) : s;
}

const plural = new Intl.PluralRules(locale);
type PluralKey = { [K in Key]: K extends `${infer B}_one` ? B : never }[Key];

/** Plural-aware translation: uses `<key>_one` or `<key>_other`, with `{count}` available. */
export function tn(key: PluralKey, count: number, vars?: Vars): string {
  return t(`${key}_${plural.select(count) === 'one' ? 'one' : 'other'}` as Key, { count, ...vars });
}

/** Like t(), but placeholders can be React elements (links, <code>, <strong>…). */
export function tx(key: Key, vars: Record<string, ReactNode>): ReactNode {
  const parts = (catalogs[lang][key] ?? en[key]).split(/\{(\w+)\}/g);
  return parts.map((p, i) => (i % 2 ? <Fragment key={i}>{vars[p] ?? `{${p}}`}</Fragment> : p));
}
