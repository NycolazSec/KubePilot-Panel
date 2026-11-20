import clsx from 'clsx';
import { ArrowUpCircle, Download, Power, Puzzle, Search, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { get, post } from '../../api';
import { Badge, Button, Card, Empty, IconButton, Loading, useLoad, useUI } from '../../components/ui';
import { t } from '../../i18n';
import { bytes } from '../../lib/format';
import { useServer } from './ServerLayout';

type Project = { id: string; slug: string; title: string; description: string; icon_url?: string };
type Installed = { file: string; size: number; disabled: boolean; project?: Project; version?: string; update?: string };
type Info = { kind: 'plugin' | 'mod'; folder: string; loaders: string[]; gameVersion: string; installed: Installed[] };
type Hit = { project_id: string; slug: string; title: string; description: string; icon_url?: string; downloads: number; author: string };

const icon = (u?: string) => (u ? `/api/modrinth/icon?u=${encodeURIComponent(u)}` : '');
const compact = (n: number) => new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n);

function ProjectIcon({ url, size = 40 }: { url?: string; size?: number }) {
  const [failed, setFailed] = useState(false);
  return url && !failed
    ? <img src={icon(url)} width={size} height={size} alt="" loading="lazy" onError={() => setFailed(true)} className="shrink-0 rounded-md bg-ink-800 object-cover" />
    : <div style={{ width: size, height: size }} className="flex shrink-0 items-center justify-center rounded-md border border-ink-700 bg-ink-800 text-ink-400"><Puzzle className="h-1/2 w-1/2" /></div>;
}

export default function Plugins() {
  const { server, state } = useServer();
  const { toast, confirm } = useUI();
  const base = `/servers/${server.id}/plugins`;
  const { data, error, loading, reload } = useLoad(() => get<Info>(base), [server.id]);
  const [q, setQ] = useState('');
  const [hits, setHits] = useState<Hit[] | null>(null);
  const [total, setTotal] = useState(0);
  const [searching, setSearching] = useState(false);
  const [busy, setBusy] = useState('');

  const search = async (query: string, offset = 0) => {
    setSearching(true);
    try {
      const r = await get<{ hits: Hit[]; total_hits: number }>(`${base}/search?q=${encodeURIComponent(query)}&offset=${offset}`);
      setHits((h) => (offset ? [...(h ?? []), ...r.hits] : r.hits));
      setTotal(r.total_hits);
    } catch (e: any) { toast(e.message, 'err'); } finally { setSearching(false); }
  };
  useEffect(() => {
    if (!data) return;
    const timer = setTimeout(() => search(q), q ? 350 : 0);
    return () => clearTimeout(timer);
  }, [q, !!data]); // eslint-disable-line react-hooks/exhaustive-deps

  const run = async (key: string, fn: () => Promise<unknown>, ok: string) => {
    setBusy(key);
    try { await fn(); toast(ok); reload(true); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };
  const install = (h: Hit) => run(h.project_id, async () => {
    const r = await post<{ installed: string[] }>(`${base}/install`, { projectId: h.project_id });
    if (r.installed.length > 1) toast(t('plugins.withDeps', { files: r.installed.join(', ') }), 'info');
  }, t('plugins.installed', { name: h.title }));

  if (error) return <Card><Empty icon={<Puzzle />} title={t('plugins.unavailable')}>{error}</Empty></Card>;
  if (loading || !data) return <Loading />;

  const kindLabel = data.kind === 'mod' ? t('plugins.mods') : t('plugins.plugins');
  const installedIds = new Set(data.installed.map((p) => p.project?.id).filter(Boolean));
  const running = state.status === 'running' || state.status === 'starting';

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2 text-xs text-ink-400">
        <Badge color="violet">{data.loaders[0]}</Badge>
        {data.gameVersion && <Badge>Minecraft {data.gameVersion}</Badge>}
        <span className="font-mono">/{data.folder}</span>
        {running && <span className="text-amber-300">· {t('plugins.restartHint')}</span>}
      </div>

      <Card padded={false} title={<>{kindLabel} <span className="ml-1 text-xs font-normal text-ink-400">{data.installed.length}</span></>}>
        {!data.installed.length ? <Empty icon={<Puzzle />} title={t('plugins.none', { kind: kindLabel.toLowerCase() })}>{t('plugins.noneHint')}</Empty> : (
          <ul className="divide-y divide-ink-800">
            {data.installed.map((p) => (
              <li key={p.file} className={clsx('flex flex-wrap items-center gap-3 px-4 py-3', p.disabled && 'opacity-60')}>
                <ProjectIcon url={p.project?.icon_url} />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate font-medium text-ink-100">{p.project?.title ?? p.file}</span>
                    {p.version && <span className="font-mono text-xs text-ink-400">{p.version}</span>}
                    {p.disabled && <Badge>{t('plugins.disabled')}</Badge>}
                    {!p.project && <Badge color="gray">{t('plugins.unknown')}</Badge>}
                  </div>
                  <p className="truncate text-xs text-ink-500">{p.file} · {bytes(p.size)}</p>
                </div>
                {p.update && (
                  <Button size="sm" variant="success" icon={<ArrowUpCircle />} loading={busy === 'u' + p.file} disabled={!!busy}
                    onClick={() => run('u' + p.file, () => post(`${base}/update`, { file: p.file }), t('plugins.updated', { version: p.update! }))}>
                    {t('plugins.update', { version: p.update })}
                  </Button>
                )}
                <IconButton title={p.disabled ? t('plugins.enable') : t('plugins.disable')} disabled={!!busy}
                  onClick={() => run('t' + p.file, () => post(`${base}/toggle`, { file: p.file }), p.disabled ? t('plugins.enabled') : t('plugins.disabledToast'))}><Power /></IconButton>
                <IconButton title={t('common.delete')} className="hover:!text-red-300" disabled={!!busy}
                  onClick={async () => { if (await confirm({ title: t('files.deleteOne', { name: p.project?.title ?? p.file }), confirm: t('common.delete'), danger: true })) run('d' + p.file, () => post(`${base}/delete`, { file: p.file }), t('files.deleted')); }}><Trash2 /></IconButton>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Card padded={false} title={t('plugins.add', { kind: kindLabel.toLowerCase() })} description={t('plugins.addHint', { version: data.gameVersion || '?' })}>
        <div className="border-b border-ink-700 p-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-500" />
            <input className="input pl-8" placeholder={t('plugins.search')} value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        </div>
        {hits === null ? <Loading /> : !hits.length ? <Empty icon={<Search />} title={t('common.noResults')} /> : (
          <>
            <ul className="grid gap-px bg-ink-800 md:grid-cols-2">
              {hits.map((h) => {
                const has = installedIds.has(h.project_id);
                return (
                  <li key={h.project_id} className="flex gap-3 bg-ink-850 p-4">
                    <ProjectIcon url={h.icon_url} size={48} />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <p className="truncate font-medium text-ink-100">{h.title}</p>
                          <p className="truncate text-xs text-ink-500">{t('mc.by', { name: h.author })} · <Download className="inline h-3 w-3" /> {compact(h.downloads)}</p>
                        </div>
                        <Button size="sm" variant={has ? 'secondary' : 'primary'} disabled={has || !!busy} loading={busy === h.project_id} onClick={() => install(h)}>
                          {has ? t('plugins.isInstalled') : t('plugins.install')}
                        </Button>
                      </div>
                      <p className="mt-1 line-clamp-2 text-xs text-ink-400">{h.description}</p>
                    </div>
                  </li>
                );
              })}
            </ul>
            {hits.length < total && (
              <div className="p-3 text-center"><Button size="sm" loading={searching} onClick={() => search(q, hits.length)}>{t('plugins.more')}</Button></div>
            )}
          </>
        )}
        <p className="border-t border-ink-700 px-4 py-2 text-2xs text-ink-500">{t('plugins.source')}</p>
      </Card>
    </div>
  );
}
