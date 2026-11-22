import { Ban, Radar, Save, Shield, ShieldAlert, ShieldCheck, ShieldOff, Unlock } from 'lucide-react';
import { useEffect, useState } from 'react';
import { del, ExposedKind, get, post, put, ShieldInfo, ShieldSettings } from '../../api';
import FirewallRules from '../../components/FirewallRules';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Tabs, Toggle, useLoad, useUI } from '../../components/ui';
import { t } from '../../i18n';
import { ago, dateTime, duration } from '../../lib/format';
import clsx from 'clsx';

/** Admin → Firewall: anti-DDoS limits, packets dropped, banned IPs and the rules for every server. */
export default function AdminFirewall() {
  const { toast } = useUI();
  const { data, error, loading, reload } = useLoad(() => get<ShieldInfo>('/admin/firewall'));
  const auditing = !!data?.exposure?.running;
  useEffect(() => {
    // every 10 s, every 3 s while the machines check each other
    const id = setInterval(() => reload(true), auditing ? 3000 : 10000);
    return () => clearInterval(id);
  }, [reload, auditing]);

  if (error) return <ErrorBox error={error} />;
  if (loading || !data) return <Loading />;
  if (!data.available) {
    return (
      <Card title={t('fw.title')}>
        <div className="flex items-start gap-3 text-sm">
          <ShieldOff className="mt-0.5 h-5 w-5 shrink-0 text-ink-400" />
          <div><p className="font-medium text-ink-100">{t('fw.off')}</p><p className="mt-1 max-w-2xl text-ink-400">{t('fw.offHint')}</p></div>
        </div>
      </Card>
    );
  }
  const addRule = async (r: { cidr: string; action: string; note: string }) => {
    try { await post('/admin/firewall/rules', r); toast(t('fw.ruleAdded')); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };
  const deleteRule = async (r: { id: number }) => {
    try { await del(`/admin/firewall/rules/${r.id}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };
  return (
    <div className="space-y-5">
      <ProtectionCard data={data} onSaved={() => reload(true)} />
      <ExposureCard data={data} reload={() => reload(true)} />
      <BlockedCard data={data} />
      <BansCard data={data} reload={() => reload(true)} />
      <Card padded={false} title={t('fw.globalRules')} description={t('fw.globalRulesHint')}>
        <FirewallRules rules={data.rules} onAdd={addRule} onDelete={deleteRule} emptyHint={t('fw.globalEmpty')} />
      </Card>
    </div>
  );
}

function ProtectionCard({ data, onSaved }: { data: ShieldInfo; onSaved: () => void }) {
  const { toast } = useUI();
  const [s, setS] = useState<ShieldSettings>(data.settings);
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try { await put('/admin/firewall', { ...s, clusterGuard: data.settings.clusterGuard }); toast(t('fw.saved')); onSaved(); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const num = (k: keyof ShieldSettings) => (e: React.ChangeEvent<HTMLInputElement>) => setS({ ...s, [k]: +e.target.value });
  const strict = s.mode === 'strict';
  const eff = data.effective;
  const nodes = data.nodes ?? [];
  return (
    <Card title={<span className="flex items-center gap-2"><Shield className="h-4 w-4 text-brand-300" />{t('fw.protection')}</span>} description={t('fw.protectionHint')}
      actions={<Button size="sm" variant="primary" icon={<Save className="h-3.5 w-3.5" />} loading={busy} onClick={save}>{t('common.save')}</Button>}>
      <div className="mb-5 space-y-1.5">
        {nodes.length === 0 ? (
          <NodeLine color="amber" label={t('fw.waiting')} detail={t('fw.waitingHint')} />
        ) : nodes.map((n) => (
          n.stale ? <NodeLine key={n.node} color="red" label={n.node} detail={t('fw.nodeStale', { ago: ago(n.updated) })} />
            : n.error ? <NodeLine key={n.node} color="red" label={n.node} detail={n.error} />
              : !n.synced ? <NodeLine key={n.node} color="amber" label={n.node} detail={t('fw.nodeApplying')} />
                : <NodeLine key={n.node} color="green" label={n.node} detail={t('fw.nodeOk', { ago: ago(n.updated) })} />
        ))}
      </div>
      <div className="mb-2">
        <Tabs value={s.mode} onChange={(mode) => setS({ ...s, mode })} items={[
          { id: 'off', label: t('fw.modeOff') }, { id: 'normal', label: t('fw.modeNormal') }, { id: 'strict', label: t('fw.modeStrict') },
        ]} />
      </div>
      <p className={clsx('mb-5 max-w-3xl text-[13px]', strict ? 'text-amber-300' : 'text-ink-400')}>
        {s.mode === 'off' ? t('fw.modeOffHint') : strict ? t('fw.modeStrictHint') : t('fw.modeNormalHint')}
      </p>
      <div className={clsx('grid max-w-4xl gap-5 sm:grid-cols-2 lg:grid-cols-4', s.mode === 'off' && 'opacity-50')}>
        <Field label={t('fw.tcpRate')} hint={strict ? t('fw.applied', { n: eff.tcpRate }) : t('fw.tcpRateHint')}>
          <input id="fw-tcp" type="number" min={1} max={1000} className="input" value={s.tcpRate} onChange={num('tcpRate')} disabled={s.mode === 'off'} />
        </Field>
        <Field label={t('fw.conns')} hint={strict ? t('fw.applied', { n: eff.conns }) : t('fw.connsHint')}>
          <input id="fw-conns" type="number" min={1} max={10000} className="input" value={s.conns} onChange={num('conns')} disabled={s.mode === 'off'} />
        </Field>
        <Field label={t('fw.udpRate')} hint={strict ? t('fw.applied', { n: eff.udpRate }) : t('fw.udpRateHint')}>
          <input id="fw-udp" type="number" min={10} max={100000} className="input" value={s.udpRate} onChange={num('udpRate')} disabled={s.mode === 'off'} />
        </Field>
        <Field label={t('fw.banMinutes')} hint={strict ? t('fw.applied', { n: eff.banMinutes }) : t('fw.banMinutesHint')}>
          <input id="fw-ban" type="number" min={1} max={10080} className="input" value={s.banMinutes} onChange={num('banMinutes')} disabled={s.mode === 'off'} />
        </Field>
      </div>
      <p className="mt-4 text-xs text-ink-500">{t(data.router ? 'fw.scopeRouter' : 'fw.scope', { ports: data.ports ?? 0 })}</p>
    </Card>
  );
}

const kindColor: Record<ExposedKind, 'green' | 'gray' | 'red' | 'yellow' | 'blue'> = {
  game: 'green', panel: 'green', sftp: 'blue', ssh: 'gray', cluster: 'red', other: 'yellow',
};

/** The cluster port guard and the exposure check: what the Internet reaches on each machine (tcpcat). */
function ExposureCard({ data, reload }: { data: ShieldInfo; reload: () => void }) {
  const { toast } = useUI();
  const [busy, setBusy] = useState(false);
  const guard = data.guard;
  const exposure = data.exposure;
  const machines = guard?.machines ?? [];
  const on = data.settings.clusterGuard;
  const setGuard = async (v: boolean) => {
    setBusy(true);
    try { await put('/admin/firewall', { ...data.settings, clusterGuard: v }); toast(t('fw.guardSaved')); reload(); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const audit = async () => {
    try { await post('/admin/firewall/audit', {}); reload(); } catch (e: any) { toast(e.message, 'err'); }
  };
  const results = exposure?.results ?? [];
  const running = !!exposure?.running;
  return (
    <Card title={<span className="flex items-center gap-2"><Radar className="h-4 w-4 text-brand-300" />{t('fw.exposure')}</span>} description={t('fw.exposureHint')}
      actions={<Button size="sm" icon={<Radar className="h-3.5 w-3.5" />} loading={running} disabled={machines.length < 2} onClick={audit}>{running ? t('fw.auditRunning') : t('fw.audit')}</Button>}>
      <div className="mb-5 rounded-md border border-ink-700 bg-ink-900 px-4 py-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2 text-sm">
            {on ? <ShieldCheck className="h-4 w-4 shrink-0 text-emerald-400" /> : <ShieldAlert className="h-4 w-4 shrink-0 text-amber-400" />}
            <span className="font-medium text-ink-100">{on ? t('fw.guardOn') : t('fw.guardOff')}</span>
          </div>
          <Toggle checked={on} onChange={(v) => { if (!busy) void setGuard(v); }} label={t('fw.guard')} />
        </div>
        <p className="mt-2 max-w-3xl text-[13px] text-ink-400">{t('fw.guardHint', { ports: (on && guard?.ports.length ? guard.ports : guard?.clusterPorts ?? []).join(', ') })}</p>
        {machines.length > 0 && (
          <p className="mt-2 text-xs text-ink-500">{t('fw.guardMachines')} {machines.map((m) => <span key={m.name} className="mr-3 font-mono text-ink-300">{m.name} {m.addrs.join(', ')}</span>)}</p>
        )}
        {on && !!guard?.joinOpenUntil && <p className="mt-2 text-xs text-amber-300">{t('fw.guardJoin', { date: dateTime(guard.joinOpenUntil) })}</p>}
        {on && <p className="mt-2 text-xs text-ink-500">{t('fw.guardOutside')}</p>}
      </div>
      <p className="mb-3 max-w-3xl text-[13px] text-ink-400">{machines.length < 2 ? t('fw.auditOneMachine') : t('fw.auditHint')}</p>
      {!results.length ? (
        running ? <Loading label={t('fw.auditRunning')} /> : <Empty icon={<Radar className="h-6 w-6" />} title={t('fw.auditNone')}>{t('fw.auditNoneHint')}</Empty>
      ) : (
        <div className="space-y-3">
          {results.map((r) => (
            <div key={r.from + r.addr} className="rounded-md border border-ink-700 px-4 py-3">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <div className="min-w-0 text-sm"><span className="font-mono text-ink-100">{r.node}</span> <span className="font-mono text-ink-400">{r.addr}</span></div>
                <div className="text-xs text-ink-500">{t('fw.auditFrom', { node: r.from, date: dateTime(r.at), probed: r.probed })}</div>
              </div>
              {r.error ? <p className="mt-2 text-sm text-red-400">{r.error}</p> : (
                <>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {r.open.length === 0 ? <span className="text-sm text-ink-400">{t('fw.auditNothing')}</span> : r.open.map((p) => (
                      <Badge key={p.port} color={kindColor[p.kind]}><span className="font-mono">{p.port}</span> {t(`fw.kind.${p.kind}` as any)}</Badge>
                    ))}
                  </div>
                  <p className={clsx('mt-2 text-xs', r.problems ? 'text-red-400' : 'text-emerald-400')}>{r.problems ? t('fw.auditProblems', { n: r.problems }) : t('fw.auditClean')}</p>
                </>
              )}
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}

const dot = { green: 'bg-emerald-400', amber: 'bg-amber-400 animate-pulse', red: 'bg-red-500' };

function NodeLine({ color, label, detail }: { color: keyof typeof dot; label: string; detail: string }) {
  return (
    <div className="flex min-w-0 items-center gap-2.5 text-sm">
      <span className={clsx('h-2 w-2 shrink-0 rounded-full', dot[color])} />
      <span className="font-mono text-ink-100">{label}</span>
      <span className="min-w-0 truncate text-ink-400">{detail}</span>
    </div>
  );
}

const counterKeys = ['blocked', 'banned', 'rate_tcp', 'conns', 'rate_udp', 'invalid', 'cluster', 'servers'] as const;

function BlockedCard({ data }: { data: ShieldInfo }) {
  const c = data.counters ?? {};
  const label: Record<(typeof counterKeys)[number], string> = {
    blocked: t('fw.cBlocked'), banned: t('fw.cBanned'), rate_tcp: t('fw.cRateTcp'), conns: t('fw.cConns'),
    rate_udp: t('fw.cRateUdp'), invalid: t('fw.cInvalid'), cluster: t('fw.cCluster'), servers: t('fw.cServers'),
  };
  return (
    <Card title={t('fw.blocked')} description={data.since ? t('fw.since', { date: dateTime(data.since) }) : undefined}>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 xl:grid-cols-8">
        {counterKeys.map((k) => (
          <div key={k} className="flex min-h-[78px] flex-col justify-between rounded-md border border-ink-700 bg-ink-900 px-3 py-2.5">
            <div className="text-2xs font-medium uppercase tracking-wider text-ink-400">{label[k]}</div>
            <div className={clsx('mt-1 text-lg font-semibold tabular-nums', (c[k] ?? 0) > 0 ? 'text-ink-100' : 'text-ink-500')}>{(c[k] ?? 0).toLocaleString()}</div>
          </div>
        ))}
      </div>
    </Card>
  );
}

function BansCard({ data, reload }: { data: ShieldInfo; reload: () => void }) {
  const { toast } = useUI();
  const [ip, setIp] = useState('');
  const [minutes, setMinutes] = useState(60);
  const [busy, setBusy] = useState(false);
  const bans = data.bans ?? [];
  const ban = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try { await post('/admin/firewall/bans', { ip: ip.trim(), minutes }); toast(t('fw.banned', { ip: ip.trim() })); setIp(''); setTimeout(reload, 6000); } catch (err: any) { toast(err.message, 'err'); } finally { setBusy(false); }
  };
  const unban = async (addr: string) => {
    try { await del(`/admin/firewall/bans/${addr}`); toast(t('fw.unbanned', { ip: addr })); setTimeout(reload, 6000); } catch (err: any) { toast(err.message, 'err'); }
  };
  return (
    <Card padded={false} title={t('fw.bans', { count: data.banCount ?? bans.length })} description={t('fw.bansHint')}>
      <form onSubmit={ban} className="flex flex-wrap items-center gap-2 border-b border-ink-700 px-5 py-4">
        <input id="fw-ban-ip" className="input min-w-0 flex-[1_1_180px] font-mono" value={ip} onChange={(e) => setIp(e.target.value)} placeholder="203.0.113.7" aria-label={t('fw.banIp')} />
        <select id="fw-ban-duration" className="input !w-auto" value={minutes} onChange={(e) => setMinutes(+e.target.value)} aria-label={t('fw.banDuration')}>
          <option value={10}>10 min</option><option value={60}>1 h</option><option value={1440}>24 h</option><option value={10080}>7 {t('unit.day')}</option>
        </select>
        <Button type="submit" icon={<Ban className="h-3.5 w-3.5" />} loading={busy} disabled={!ip.trim()}>{t('fw.ban')}</Button>
      </form>
      {!bans.length ? <Empty icon={<Ban className="h-6 w-6" />} title={t('fw.noBans')}>{t('fw.noBansHint')}</Empty> : (
        <div className="max-h-96 overflow-auto">
          <table className="w-full text-sm">
            <thead><tr className="border-b border-ink-700"><th className="th">{t('fw.ip')}</th><th className="th">{t('fw.expiresIn')}</th><th className="w-12" /></tr></thead>
            <tbody>
              {bans.map((b) => (
                <tr key={b.ip} className="border-b border-ink-800 last:border-0">
                  <td className="px-4 py-2.5 font-mono text-ink-100">{b.ip}</td>
                  <td className="px-4 py-2.5 tabular-nums text-ink-300">{b.expires ? duration(b.expires) : '—'}</td>
                  <td className="px-4 py-2 text-right"><IconButton title={t('fw.unban')} onClick={() => unban(b.ip)}><Unlock className="h-4 w-4" /></IconButton></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
