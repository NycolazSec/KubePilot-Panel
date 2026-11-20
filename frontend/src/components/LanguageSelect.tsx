import { Languages } from 'lucide-react';
import { lang, languages, Lang, setLang, t } from '../i18n';

/** Language picker: saves the choice in this browser and reloads the interface. */
export default function LanguageSelect({ compact }: { compact?: boolean }) {
  return (
    <label className="inline-flex items-center gap-2 text-[13px] text-ink-300">
      <Languages className="h-4 w-4 text-ink-400" />
      <span className="sr-only">{t('account.language')}</span>
      <select className={compact ? 'input !h-7 !w-auto !py-0 text-xs' : 'input !w-auto'} value={lang} onChange={(e) => setLang(e.target.value as Lang)}>
        {(Object.keys(languages) as Lang[]).map((l) => <option key={l} value={l}>{languages[l]}</option>)}
      </select>
    </label>
  );
}
