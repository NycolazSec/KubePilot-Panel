import { FileCog } from 'lucide-react';
import { useState } from 'react';
import { ConfigFile } from '../api';
import { t } from '../i18n';

/** Configuration files edited before each start (eggs' config.files): a summary, and the JSON for advanced edits. */
export default function ConfigFilesField({ value, onChange }: { value: ConfigFile[]; onChange: (v: ConfigFile[]) => void }) {
  const [text, setText] = useState(() => JSON.stringify(value, null, 2));
  const [err, setErr] = useState('');
  const [open, setOpen] = useState(false);
  const update = (s: string) => {
    setText(s);
    try {
      const v = s.trim() ? JSON.parse(s) : [];
      if (!Array.isArray(v)) throw new Error();
      setErr('');
      onChange(v);
    } catch { setErr(t('cfgfiles.invalid')); }
  };
  const preview = (raw: unknown) => (typeof raw === 'string' ? raw : JSON.stringify(raw));
  return (
    <div className="space-y-2">
      {value.length === 0 ? <p className="text-xs text-ink-500">{t('cfgfiles.none')}</p> : (
        <ul className="space-y-1.5">
          {value.map((f) => (
            <li key={f.file} className="rounded-md border border-ink-700 px-3 py-2 text-xs">
              <div className="flex items-center gap-2"><FileCog className="h-3.5 w-3.5 text-ink-400" /><span className="font-mono text-ink-100">{f.file}</span><span className="text-ink-500">{f.parser}</span></div>
              <p className="mt-1 truncate font-mono text-ink-400" title={f.find.map((r) => `${r.match} = ${preview(r.value)}`).join('\n')}>
                {f.find.slice(0, 4).map((r) => `${r.match} = ${preview(r.value)}`).join(' · ')}{f.find.length > 4 ? ` · +${f.find.length - 4}` : ''}
              </p>
            </li>
          ))}
        </ul>
      )}
      <button type="button" className="text-xs text-brand-300 hover:underline" onClick={() => setOpen(!open)}>{open ? t('cfgfiles.hideJson') : t('cfgfiles.editJson')}</button>
      {open && (
        <>
          <textarea className="input min-h-[160px] font-mono text-xs" value={text} onChange={(e) => update(e.target.value)} spellCheck={false} />
          {err && <p className="text-xs text-red-300">{err}</p>}
        </>
      )}
    </div>
  );
}
