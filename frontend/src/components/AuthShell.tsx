import LanguageSelect from './LanguageSelect';
import { t } from '../i18n';

/** Frame of the signed-out pages: logo, title, card, language. */
export default function AuthShell({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-full flex-col items-center justify-center bg-ink-950 px-4 py-12">
      <div className="w-full max-w-[360px]">
        <div className="mb-8 flex flex-col items-center text-center">
          <img src="/kubepilot.svg" alt="" width={48} height={48} />
          <h1 className="mt-4 text-xl font-semibold tracking-tight">{title}</h1>
          {subtitle && <p className="mt-1 text-[13px] text-ink-400">{subtitle}</p>}
        </div>
        {children}
        <p className="mt-6 text-center text-2xs text-ink-500">{t('login.footer')}</p>
        <div className="mt-4 flex justify-center"><LanguageSelect compact /></div>
      </div>
    </div>
  );
}
