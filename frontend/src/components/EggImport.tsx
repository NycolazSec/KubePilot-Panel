import { Download, Egg, Link as LinkIcon, Search, Upload } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { get, post, Template } from '../api';
import { t } from '../i18n';
import { Button, Empty, ErrorBox, Field, Loading, Modal, Tabs, useUI } from './ui';

type Entry = { repo: string; name: string; group: string; url: string };
export type EggResult = { template: Template; warnings: string[] };

/** Import a Pterodactyl / Pelican egg: community catalog, GitHub link or file. Returns a template draft. */
export default function EggImport({ open, onClose, onConverted }: { open: boolean; onClose: () => void; onConverted: (r: EggResult) => void }) {
  const { toast } = useUI();
  const [tab, setTab] = useState<'catalog' | 'link'>('catalog');
  const [catalog, setCatalog] = useState<Entry[] | null>(null);
  const [error, setError] = useState('');
  const [q, setQ] = useState('');
  const [repo, setRepo] = useState('');
  const [link, setLink] = useState('');
  const [busy, setBusy] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open || catalog) return;
    get<Entry[]>('/admin/eggs/catalog').then(setCatalog).catch((e) => setError(e.message));
  }, [open, catalog]);

  const convert = async (key: string, body: unknown) => {
    setBusy(key);
    try { onConverted(await post<EggResult>('/admin/templates/egg', body)); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };

  const repos = useMemo(() => [...new Set((catalog ?? []).map((e) => e.repo))].sort(), [catalog]);
  const list = useMemo(() => (catalog ?? []).filter((e) =>
    (!repo || e.repo === repo) && (!q || `${e.name} ${e.group}`.toLowerCase().includes(q.toLowerCase()))).slice(0, 200), [catalog, q, repo]);

  return (
    <Modal open={open} onClose={onClose} wide="xl" title={<span className="inline-flex items-center gap-2"><Egg className="h-4 w-4" />{t('eggs.title')}</span>}>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <Tabs value={tab} onChange={setTab} items={[{ id: 'catalog', label: t('eggs.catalog') }, { id: 'link', label: t('eggs.linkOrFile') }]} />
        <p className="text-xs text-ink-400">{t('eggs.hint')}</p>
      </div>
      {tab === 'catalog' ? (
        error ? <ErrorBox error={error} /> : !catalog ? <Loading /> : (
          <>
            <div className="mb-3 flex flex-wrap gap-2">
              <div className="relative min-w-[200px] flex-1">
                <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-500" />
                <input className="input pl-8" autoFocus placeholder={t('eggs.search', { count: catalog.length })} value={q} onChange={(e) => setQ(e.target.value)} />
              </div>
              <select className="input !w-auto" value={repo} onChange={(e) => setRepo(e.target.value)}>
                <option value="">{t('eggs.allCategories')}</option>
                {repos.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
            </div>
            {!list.length ? <Empty icon={<Search />} title={t('common.noResults')} /> : (
              <ul className="grid max-h-[55vh] gap-px overflow-y-auto rounded-md border border-ink-700 bg-ink-800 sm:grid-cols-2">
                {list.map((e) => (
                  <li key={e.url} className="flex items-center justify-between gap-3 bg-ink-850 px-3 py-2.5">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium text-ink-100">{e.name}</p>
                      <p className="truncate text-xs text-ink-500">{e.group}</p>
                    </div>
                    <Button size="sm" icon={<Download />} loading={busy === e.url} disabled={!!busy} onClick={() => convert(e.url, { url: e.url })}>{t('eggs.import')}</Button>
                  </li>
                ))}
              </ul>
            )}
            <p className="mt-2 text-2xs text-ink-500">{t('eggs.source')}</p>
          </>
        )
      ) : (
        <div className="space-y-5">
          <form onSubmit={(e) => { e.preventDefault(); if (link.trim()) convert('link', { url: link.trim() }); }}>
            <Field label={t('eggs.link')} hint={t('eggs.linkHint')}>
              <div className="flex gap-2">
                <input className="input font-mono text-xs" value={link} onChange={(e) => setLink(e.target.value)} placeholder="https://github.com/pelican-eggs/…/egg-….json" />
                <Button type="submit" variant="primary" icon={<LinkIcon />} loading={busy === 'link'}>{t('eggs.import')}</Button>
              </div>
            </Field>
          </form>
          <div>
            <span className="label">{t('eggs.file')}</span>
            <Button icon={<Upload />} loading={busy === 'file'} onClick={() => fileRef.current?.click()}>{t('eggs.chooseFile')}</Button>
            <input ref={fileRef} type="file" accept="application/json,.json" hidden onChange={async (e) => {
              const f = e.target.files?.[0];
              e.target.value = '';
              if (f) convert('file', { egg: await f.text() });
            }} />
          </div>
        </div>
      )}
    </Modal>
  );
}
