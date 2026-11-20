import clsx from 'clsx';
import { Ban, Crown, DoorOpen, Pickaxe, Plus, RotateCcw, ShieldCheck, ShieldOff, UserCheck, UserMinus, Users, Wifi } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { get, post } from '../../api';
import MinecraftText from '../../components/minecraft/MinecraftText';
import PlayerHead from '../../components/minecraft/PlayerHead';
import { Badge, Bar, Button, Card, Empty, ErrorBox, IconButton, Loading, Tabs, useLoad, useUI } from '../../components/ui';
import { t } from '../../i18n';
import { dateTime } from '../../lib/format';
import { useServer } from './ServerLayout';

type Player = { name: string; uuid?: string };
type Entry = { name?: string; uuid?: string; xuid?: string; ip?: string; level?: number; reason?: string; source?: string; created?: string; expires?: string };
type MC = {
  kind: 'minecraft-java' | 'minecraft-bedrock'; running: boolean; rcon: boolean;
  status: { version?: string; motd?: unknown; favicon?: string; playersOnline: number; playersMax: number; players: Player[]; latencyMs: number } | null;
  whitelist: Entry[]; ops: Entry[]; bans: Entry[]; ipBans: Entry[]; properties: Record<string, string>;
};
type ListTab = 'whitelist' | 'ops' | 'bans' | 'ipBans';

const same = (a: Entry | Player, b: Player) =>
  (a.uuid && b.uuid && a.uuid.replace(/-/g, '').toLowerCase() === b.uuid.replace(/-/g, '').toLowerCase()) || a.name?.toLowerCase() === b.name.toLowerCase();

export default function Players() {
  const { server } = useServer();
  const { toast, prompt, confirm } = useUI();
  const { data, error, loading, reload } = useLoad(() => get<MC>(`/servers/${server.id}/minecraft`), [server.id]);
  const [tab, setTab] = useState<ListTab>('whitelist');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState('');

  useEffect(() => {
    const timer = setInterval(() => reload(true), 10000);
    return () => clearInterval(timer);
  }, [reload]);

  const java = data?.kind === 'minecraft-java';
  const act = async (action: string, player = '', reason = '') => {
    setBusy(action + player);
    try {
      const r = await post<{ output: string }>(`/servers/${server.id}/minecraft/action`, { action, player, reason });
      toast(r.output || t('mc.done'));
      setTimeout(() => reload(true), 600);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };
  const withReason = async (action: 'kick' | 'ban', p: Player) => {
    const reason = await prompt(action === 'kick' ? t('mc.kickTitle', { name: p.name }) : t('mc.banTitle', { name: p.name }), '', t('mc.reason'));
    if (reason !== null) act(action, p.name, reason);
  };

  const lists = useMemo(() => {
    if (!data) return [];
    const all: { id: ListTab; label: string; entries: Entry[] }[] = [{ id: 'whitelist', label: java ? t('mc.whitelist') : t('mc.allowlist'), entries: data.whitelist }];
    if (java) all.push({ id: 'ops', label: t('mc.ops'), entries: data.ops }, { id: 'bans', label: t('mc.bans'), entries: data.bans }, { id: 'ipBans', label: t('mc.ipBans'), entries: data.ipBans });
    return all;
  }, [data, java]);

  if (error) return <ErrorBox error={error} />;
  if (loading || !data) return <Loading />;

  const st = data.status;
  const online = st?.players ?? [];
  const whitelistOn = data.properties['white-list'] === 'true';
  const current = lists.find((l) => l.id === tab) ?? lists[0];
  const off = !data.running;
  const addAction: Record<ListTab, string> = { whitelist: 'whitelist-add', ops: 'op', bans: 'ban', ipBans: '' };
  const removeAction: Record<ListTab, string> = { whitelist: 'whitelist-remove', ops: 'deop', bans: 'pardon', ipBans: 'pardon-ip' };

  return (
    <div className="space-y-5">
      {/* status */}
      <section className="card flex flex-wrap items-center gap-4 p-4">
        {st?.favicon
          ? <img src={st.favicon} width={64} height={64} alt="" className="rounded-md" style={{ imageRendering: 'pixelated' }} />
          : <div className="flex h-16 w-16 items-center justify-center rounded-md border border-ink-600 bg-ink-800 text-ink-300"><Pickaxe className="h-7 w-7" /></div>}
        <div className="min-w-0 flex-1">
          {st ? (
            <>
              <div className="rounded-md bg-ink-950 px-3 py-2 font-mono text-[13px] leading-snug text-[#AAAAAA]"><MinecraftText value={st.motd ?? ''} /></div>
              <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-400">
                {st.version && <Badge color="violet">{st.version}</Badge>}
                <span className="inline-flex items-center gap-1"><Wifi className="h-3.5 w-3.5" /> {st.latencyMs} ms</span>
                {java && !data.rcon && <span title={t('mc.noRconHint')}>{t('mc.noRcon')}</span>}
              </div>
            </>
          ) : (
            <p className="text-[13px] text-ink-400">{off ? t('mc.offline') : t('mc.noAnswer')}</p>
          )}
        </div>
        <div className="w-full sm:w-48">
          <div className="mb-1 flex items-baseline justify-between text-xs text-ink-400"><span>{t('mc.players')}</span><span className="text-lg font-semibold tabular-nums text-ink-100">{st ? `${st.playersOnline} / ${st.playersMax}` : '—'}</span></div>
          <Bar value={st?.playersOnline ?? 0} max={st?.playersMax || 1} />
        </div>
      </section>

      {/* online players */}
      <Card padded={false} title={<>{t('mc.online')} {st && <span className="ml-1 text-xs font-normal text-ink-400">{st.playersOnline}</span>}</>}>
        {off ? <Empty icon={<Users />} title={t('mc.offline')}>{t('mc.offlineHint')}</Empty>
          : !online.length ? <Empty icon={<Users />} title={st?.playersOnline ? t('mc.hiddenPlayers') : t('mc.nobody')}>{st?.playersOnline ? t('mc.hiddenHint') : t('mc.nobodyHint')}</Empty> : (
            <ul className="grid gap-px bg-ink-800 sm:grid-cols-2 xl:grid-cols-3">
              {online.map((p) => {
                const isOp = data.ops.some((o) => same(o, p));
                const listed = data.whitelist.some((w) => same(w, p));
                return (
                  <li key={p.uuid || p.name} className="flex items-center gap-3 bg-ink-850 px-4 py-3">
                    <PlayerHead uuid={p.uuid} name={p.name} size={40} />
                    <div className="min-w-0 flex-1">
                      <p className="truncate font-medium text-ink-100">{p.name}</p>
                      <div className="mt-0.5 flex gap-1">
                        {isOp && <Badge color="yellow"><Crown /> OP</Badge>}
                        {listed && <Badge color="green"><UserCheck /> {java ? t('mc.whitelisted') : t('mc.allowlisted')}</Badge>}
                      </div>
                    </div>
                    <div className="flex shrink-0 gap-0.5">
                      {isOp
                        ? <IconButton title={t('mc.deop')} disabled={!!busy} onClick={() => act('deop', p.name)}><ShieldOff /></IconButton>
                        : <IconButton title={t('mc.op')} disabled={!!busy} onClick={() => act('op', p.name)}><Crown /></IconButton>}
                      <IconButton title={t('mc.kick')} disabled={!!busy} onClick={() => withReason('kick', p)}><DoorOpen /></IconButton>
                      {java && <IconButton title={t('mc.ban')} disabled={!!busy} className="hover:!text-red-300" onClick={() => withReason('ban', p)}><Ban /></IconButton>}
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
      </Card>

      {/* whitelist / ops / bans */}
      <Card padded={false}
        title={<Tabs value={current.id} onChange={setTab} items={lists.map((l) => ({ id: l.id, label: <>{l.label} <span className="ml-1 text-ink-500">{l.entries.length}</span></> }))} />}
        actions={current.id === 'whitelist' && java && (
          <Button size="sm" variant={whitelistOn ? 'success' : 'secondary'} icon={<ShieldCheck />} disabled={off || !!busy} loading={busy.startsWith('whitelist-o')}
            onClick={async () => { if (!whitelistOn || await confirm({ title: t('mc.whitelistOffTitle'), body: t('mc.whitelistOffBody'), confirm: t('mc.disable') })) act(whitelistOn ? 'whitelist-off' : 'whitelist-on'); }}>
            {whitelistOn ? t('mc.whitelistOn') : t('mc.whitelistOff')}
          </Button>
        )}>
        {current.id !== 'ipBans' && (
          <form className="flex gap-2 border-b border-ink-700 p-3" onSubmit={(e) => { e.preventDefault(); if (name.trim()) { act(addAction[current.id], name.trim()); setName(''); } }}>
            <input className="input" placeholder={t('mc.playerName')} value={name} onChange={(e) => setName(e.target.value)} maxLength={17} disabled={off} />
            <Button type="submit" variant="primary" icon={<Plus />} disabled={off || !name.trim()}>{t(current.id === 'bans' ? 'mc.ban' : current.id === 'ops' ? 'mc.op' : 'common.add')}</Button>
          </form>
        )}
        {off && <p className="border-b border-ink-700 px-4 py-2 text-xs text-ink-400">{t('mc.startToEdit')}</p>}
        {!current.entries.length ? <Empty icon={<Users />} title={t('mc.emptyList')} /> : (
          <ul className="divide-y divide-ink-800">
            {current.entries.map((e, i) => {
              const label = e.name || e.ip || e.xuid || '?';
              return (
                <li key={(e.uuid || e.ip || e.name || '') + i} className="flex items-center gap-3 px-4 py-2.5">
                  {current.id === 'ipBans' ? <div className="flex h-8 w-8 items-center justify-center rounded bg-ink-800 text-ink-400"><Ban className="h-4 w-4" /></div>
                    : <PlayerHead uuid={e.uuid} name={label} size={32} />}
                  <div className="min-w-0 flex-1">
                    <p className={clsx('truncate text-sm text-ink-100', current.id === 'ipBans' && 'font-mono')}>{label}
                      {current.id === 'ops' && e.level ? <span className="ml-2 text-xs text-ink-500">{t('mc.level', { level: e.level })}</span> : null}
                      {online.some((p) => same(e, p)) && <span className="ml-2 inline-block h-1.5 w-1.5 rounded-full bg-emerald-400 align-middle" title={t('mc.online')} />}
                    </p>
                    {(e.reason || e.created) && (
                      <p className="truncate text-xs text-ink-400">
                        {e.reason}{e.source && ` · ${t('mc.by', { name: e.source })}`}{e.created && ` · ${dateTime(Date.parse(e.created.replace(' ', 'T').replace(/ ([+-]\d{2})(\d{2})$/, '$1:$2')) / 1000 || 0)}`}
                        {e.expires && e.expires !== 'forever' && ` · ${t('mc.until', { date: e.expires })}`}
                      </p>
                    )}
                  </div>
                  <IconButton title={current.id === 'bans' || current.id === 'ipBans' ? t('mc.pardon') : current.id === 'ops' ? t('mc.deop') : t('network.remove')}
                    disabled={off || !!busy} onClick={() => act(removeAction[current.id], current.id === 'ipBans' ? (e.ip ?? '') : label)}>
                    {current.id === 'bans' || current.id === 'ipBans' ? <RotateCcw /> : <UserMinus />}
                  </IconButton>
                </li>
              );
            })}
          </ul>
        )}
      </Card>
    </div>
  );
}
