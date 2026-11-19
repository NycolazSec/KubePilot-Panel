import clsx from 'clsx';
import { ExternalLink, FileText, Lock, Minus, Play, Plus, RotateCw, Search, Square, Trash2, TriangleAlert } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { get, post } from '../../api';
import LogViewer, { LogTarget } from '../../components/LogViewer';
import { Badge, Empty, ErrorBox, IconButton, Loading, Tabs, Toggle, useLoad, useUI } from '../../components/ui';
import { ago, bytes } from '../../lib/format';

type NS = { name: string; system: boolean; canManage: boolean };
type Workload = { kind: string; namespace: string; name: string; desired: number; ready: number; images: string[]; createdAt: number; serverId?: string; savedReplicas?: number };
type Pod = { namespace: string; name: string; status: string; ready: number; total: number; restarts: number; node: string; ip: string; createdAt: number; containers: string[]; owner: string; cpu?: number; mem?: number; serverId?: string; stuck: boolean };
type Ingress = { namespace: string; name: string; host: string; tls: boolean; backends: string[] };
type Service = { namespace: string; name: string; type: string; clusterIP: string; ports: string[] };
type Event = { namespace: string; object: string; reason: string; message: string; count: number; last: number };
type Overview = { namespaces: NS[]; workloads: Workload[]; pods: Pod[]; ingresses: Ingress[]; services: Service[]; events: Event[]; metrics: boolean; gameNamespace: string };

const HIDE_KEY = 'kubepilot-cluster-hide-system';

function podColor(p: Pod): 'green' | 'yellow' | 'red' | 'gray' {
  if (p.stuck) return 'red';
  if (p.status === 'Running') return p.ready === p.total ? 'green' : 'yellow';
  if (p.status === 'Succeeded' || p.status === 'Completed') return 'gray';
  if (p.status === 'Pending' || p.status === 'ContainerCreating' || p.status === 'Terminating' || p.status.startsWith('Init')) return 'yellow';
  return 'red';
}

const kindShort: Record<string, string> = { Deployment: 'Deploy', StatefulSet: 'STS', DaemonSet: 'DS' };

function Stat({ label, value, sub, tone }: { label: string; value: React.ReactNode; sub?: string; tone?: 'red' | 'green' }) {
  return (
    <div className="card px-4 py-3">
      <p className="text-2xs font-medium uppercase tracking-wider text-ink-400">{label}</p>
      <p className={clsx('mt-1 text-lg font-semibold tabular-nums', tone === 'red' && 'text-red-300', tone === 'green' && 'text-emerald-300')}>{value}</p>
      {sub && <p className="text-xs text-ink-400">{sub}</p>}
    </div>
  );
}

export default function AdminCluster() {
  const { toast, confirm } = useUI();
  const { data, error, loading, reload } = useLoad(() => get<Overview>('/admin/cluster'));
  const [tab, setTab] = useState<'apps' | 'pods' | 'network' | 'events'>('apps');
  const [ns, setNs] = useState('');
  const [q, setQ] = useState('');
  const [hideSystem, setHideSystem] = useState(() => { try { return localStorage.getItem(HIDE_KEY) !== '0'; } catch { return true; } });
  const [logs, setLogs] = useState<LogTarget | null>(null);
  const [busy, setBusy] = useState('');

  useEffect(() => {
    const t = setInterval(() => !logs && reload(true), 10000);
    return () => clearInterval(t);
  }, [reload, logs]);
  useEffect(() => { try { localStorage.setItem(HIDE_KEY, hideSystem ? '1' : '0'); } catch { /* ignore */ } }, [hideSystem]);

  const nsInfo = useMemo(() => Object.fromEntries((data?.namespaces || []).map((n) => [n.name, n])), [data]);
  const visible = (namespace: string, text: string) =>
    (!ns || ns === namespace) && (!hideSystem || ns === namespace || !nsInfo[namespace]?.system) && (!q || text.toLowerCase().includes(q.toLowerCase()));

  const workloads = (data?.workloads || []).filter((w) => visible(w.namespace, `${w.namespace} ${w.name} ${w.images.join(' ')}`));
  const pods = (data?.pods || []).filter((p) => visible(p.namespace, `${p.namespace} ${p.name} ${p.owner} ${p.node} ${p.status}`));
  const ingresses = (data?.ingresses || []).filter((i) => visible(i.namespace, `${i.namespace} ${i.host} ${i.name} ${i.backends.join(' ')}`));
  const services = (data?.services || []).filter((s) => visible(s.namespace, `${s.namespace} ${s.name} ${s.type}`));
  const events = (data?.events || []).filter((e) => visible(e.namespace, `${e.namespace} ${e.object} ${e.reason} ${e.message}`));

  const act = async (key: string, fn: () => Promise<unknown>, ok: string) => {
    setBusy(key);
    try { await fn(); toast(ok); setTimeout(() => reload(true), 800); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };
  const podsOf = (w: Workload) => (data?.pods || []).filter((p) => p.namespace === w.namespace && (p.owner === `${w.kind}/${w.name}`));
  const openLogs = (namespace: string, list: Pod[], pod?: string) => {
    if (!list.length) return toast('Aucun pod pour cette application', 'info');
    setLogs({ namespace, pods: list.map((p) => ({ name: p.name, containers: p.containers })), pod });
  };
  const restart = async (w: Workload) => {
    if (!(await confirm({ title: `Redémarrer ${w.name} ?`, body: <>Les pods de <b>{w.namespace}/{w.name}</b> sont recréés un par un, en attendant que chacun soit prêt. Avec une seule réplique, l'appli est indisponible quelques secondes.</>, confirm: 'Redémarrer' }))) return;
    act(`r-${w.name}`, () => post('/admin/cluster/restart', { namespace: w.namespace, kind: w.kind, name: w.name }), `Redémarrage de ${w.name} lancé`);
  };
  const scale = async (w: Workload, n: number | null) => {
    if (n === 0 && !(await confirm({ title: `Arrêter ${w.name} ?`, body: <>L'application passe à 0 réplique : elle ne répond plus jusqu'à ce que tu la redémarres. Le nombre actuel ({w.desired}) est mémorisé.</>, confirm: 'Arrêter', danger: true }))) return;
    act(`s-${w.name}`, () => post('/admin/cluster/scale', { namespace: w.namespace, kind: w.kind, name: w.name, replicas: n ?? undefined }),
      n === null ? `${w.name} démarré` : n === 0 ? `${w.name} arrêté` : `${w.name} → ${n} réplique(s)`);
  };
  const deletePod = async (p: Pod) => {
    const force = p.stuck || p.status === 'Terminating';
    if (!(await confirm({
      title: force ? `Forcer la suppression de ${p.name} ?` : `Supprimer ${p.name} ?`, danger: true, confirm: force ? 'Forcer' : 'Supprimer',
      body: force ? 'Le pod est bloqué en suppression (souvent parce que son nœud est hors service). Il sera retiré immédiatement de Kubernetes.' : 'Le pod est supprimé ; s\'il appartient à une application, Kubernetes en recrée un aussitôt.',
    }))) return;
    act(`p-${p.name}`, () => post('/admin/cluster/delete-pod', { namespace: p.namespace, name: p.name, force }), 'Pod supprimé');
  };

  if (error) return <ErrorBox error={error} />;
  if (loading || !data) return <Loading label="Lecture du cluster…" />;

  const healthy = data.workloads.filter((w) => w.ready >= w.desired && w.desired > 0).length;
  const running = data.pods.filter((p) => p.status === 'Running').length;
  const stuck = data.pods.filter((p) => p.stuck).length;
  const failing = data.pods.filter((p) => podColor(p) === 'red' && !p.stuck).length;
  const readOnly = (namespace: string) => !nsInfo[namespace]?.canManage;
  const ReadOnly = () => <span className="inline-flex items-center gap-1 text-2xs text-ink-500" title="Namespace en lecture seule"><Lock className="h-3 w-3" />Lecture seule</span>;

  return (
    <>
      <div className="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat label="Applications" value={`${healthy} / ${data.workloads.length}`} sub="en bonne santé" tone={healthy === data.workloads.length ? 'green' : undefined} />
        <Stat label="Pods" value={`${running} / ${data.pods.length}`} sub="en cours d'exécution" />
        <Stat label="Pods en erreur" value={failing} sub={failing ? 'à vérifier' : 'aucun'} tone={failing ? 'red' : undefined} />
        <Stat label="Alertes" value={data.events.length} sub="événements Warning récents" tone={data.events.length ? 'red' : undefined} />
      </div>

      {stuck > 0 && (
        <div className="mb-4 flex items-start gap-2.5 rounded-md border border-amber-500/30 bg-amber-500/[.07] px-3.5 py-2.5 text-[13px] text-amber-100">
          <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-300" />
          <span><b>{stuck} pod(s) bloqués en suppression</b>, en général sur un nœud hors service. Ils ne consomment rien mais polluent l'état du cluster ; tu peux les forcer depuis l'onglet Pods (namespaces autorisés seulement).</span>
        </div>
      )}

      <section className="card">
        <div className="flex flex-wrap items-center gap-2 border-b border-ink-700 p-3">
          <Tabs value={tab} onChange={setTab} items={[
            { id: 'apps', label: `Applications (${workloads.length})` }, { id: 'pods', label: `Pods (${pods.length})` },
            { id: 'network', label: `Réseau (${ingresses.length})` }, { id: 'events', label: `Alertes (${events.length})` },
          ]} />
          <div className="relative min-w-[180px] flex-1 sm:max-w-xs">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-500" />
            <input className="input pl-8" placeholder="Filtrer…" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <select className="input !w-auto" value={ns} onChange={(e) => setNs(e.target.value)}>
            <option value="">Tous les namespaces</option>
            {data.namespaces.map((n) => <option key={n.name} value={n.name}>{n.name}{n.canManage ? '' : ' (lecture)'}</option>)}
          </select>
          <div className="ml-auto"><Toggle checked={hideSystem} onChange={setHideSystem} label="Masquer le système" /></div>
        </div>

        <div className="overflow-x-auto">
          {tab === 'apps' && (workloads.length === 0 ? <Empty title="Aucune application" /> : (
            <table className="w-full">
              <thead className="border-b border-ink-700"><tr>
                <th className="th">Application</th><th className="th">Prêts</th><th className="th hidden lg:table-cell">Image</th><th className="th hidden md:table-cell">Âge</th><th className="th text-right">Actions</th>
              </tr></thead>
              <tbody className="divide-y divide-ink-700/70">
                {workloads.map((w) => {
                  const ok = w.ready >= w.desired && w.desired > 0;
                  const ro = readOnly(w.namespace);
                  const scalable = w.kind !== 'DaemonSet';
                  return (
                    <tr key={`${w.kind}/${w.namespace}/${w.name}`} className="hover:bg-ink-800/40">
                      <td className="td">
                        <div className="flex items-center gap-2.5">
                          <span className="w-12 shrink-0 rounded border border-ink-600 bg-ink-800 py-0.5 text-center font-mono text-2xs text-ink-400">{kindShort[w.kind]}</span>
                          <div className="min-w-0">
                            <p className="truncate font-medium">{w.name}</p>
                            <p className="truncate text-xs text-ink-400">{w.namespace}</p>
                          </div>
                          {w.serverId && <Badge color="violet">Serveur KubePilot</Badge>}
                        </div>
                      </td>
                      <td className="td"><Badge color={w.desired === 0 ? 'gray' : ok ? 'green' : w.ready > 0 ? 'yellow' : 'red'}>{w.desired === 0 ? 'Arrêté' : `${w.ready} / ${w.desired}`}</Badge></td>
                      <td className="td hidden max-w-[320px] lg:table-cell"><p className="truncate font-mono text-xs text-ink-300" title={w.images.join('\n')}>{w.images.join(', ')}</p></td>
                      <td className="td hidden text-xs text-ink-400 md:table-cell">{ago(w.createdAt)}</td>
                      <td className="td">
                        <div className="flex items-center justify-end gap-1">
                          <IconButton title="Logs" onClick={() => openLogs(w.namespace, podsOf(w))}><FileText /></IconButton>
                          {w.serverId ? (
                            <Link to={`/server/${w.serverId}`} title="Ouvrir dans KubePilot" className="inline-flex h-7 w-7 items-center justify-center rounded-md text-ink-400 hover:bg-ink-750 hover:text-ink-100"><ExternalLink className="h-4 w-4" /></Link>
                          ) : ro ? <ReadOnly /> : <>
                            {scalable && w.desired > 0 && <IconButton title="Retirer une réplique" disabled={!!busy || w.desired <= 1} onClick={() => scale(w, w.desired - 1)}><Minus /></IconButton>}
                            {scalable && w.desired > 0 && <span className="w-5 text-center text-xs tabular-nums text-ink-300">{w.desired}</span>}
                            {scalable && w.desired > 0 && <IconButton title="Ajouter une réplique" disabled={!!busy || w.desired >= 20} onClick={() => scale(w, w.desired + 1)}><Plus /></IconButton>}
                            <IconButton title="Redémarrer (pod par pod)" disabled={!!busy || w.desired === 0} onClick={() => restart(w)}><RotateCw className={clsx(busy === `r-${w.name}` && 'animate-spin')} /></IconButton>
                            {scalable && (w.desired > 0
                              ? <IconButton title="Arrêter (0 réplique)" className="hover:!text-red-300" disabled={!!busy} onClick={() => scale(w, 0)}><Square /></IconButton>
                              : <IconButton title={`Démarrer (${w.savedReplicas || 1} réplique(s))`} className="hover:!text-emerald-300" disabled={!!busy} onClick={() => scale(w, null)}><Play /></IconButton>)}
                          </>}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          ))}

          {tab === 'pods' && (pods.length === 0 ? <Empty title="Aucun pod" /> : (
            <table className="w-full">
              <thead className="border-b border-ink-700"><tr>
                <th className="th">Pod</th><th className="th">Statut</th><th className="th hidden sm:table-cell">Prêts</th><th className="th hidden md:table-cell">Redém.</th>
                <th className="th hidden lg:table-cell">CPU</th><th className="th hidden lg:table-cell">Mémoire</th><th className="th hidden xl:table-cell">Nœud</th><th className="th hidden md:table-cell">Âge</th><th className="th text-right">Actions</th>
              </tr></thead>
              <tbody className="divide-y divide-ink-700/70">
                {pods.map((p) => (
                  <tr key={`${p.namespace}/${p.name}`} className="hover:bg-ink-800/40">
                    <td className="td"><p className="max-w-[340px] truncate font-mono text-xs text-ink-100">{p.name}</p><p className="truncate text-xs text-ink-400">{p.namespace}{p.owner && ` · ${p.owner}`}</p></td>
                    <td className="td"><Badge color={podColor(p)}>{p.stuck ? 'Bloqué' : p.status}</Badge></td>
                    <td className="td hidden text-xs tabular-nums sm:table-cell">{p.ready}/{p.total}</td>
                    <td className="td hidden text-xs tabular-nums md:table-cell"><span className={clsx(p.restarts > 5 && 'text-red-300')}>{p.restarts}</span></td>
                    <td className="td hidden text-xs tabular-nums text-ink-300 lg:table-cell">{p.cpu != null ? `${Math.round(p.cpu)}m` : '—'}</td>
                    <td className="td hidden text-xs tabular-nums text-ink-300 lg:table-cell">{p.mem != null ? bytes(p.mem, 0) : '—'}</td>
                    <td className="td hidden text-xs text-ink-400 xl:table-cell">{p.node || '—'}</td>
                    <td className="td hidden text-xs text-ink-400 md:table-cell">{ago(p.createdAt)}</td>
                    <td className="td">
                      <div className="flex items-center justify-end gap-1">
                        <IconButton title="Logs" onClick={() => openLogs(p.namespace, [p], p.name)}><FileText /></IconButton>
                        {p.serverId ? null : readOnly(p.namespace) ? <ReadOnly /> :
                          <IconButton title={p.stuck || p.status === 'Terminating' ? 'Forcer la suppression' : 'Supprimer (recréé par son application)'} className="hover:!text-red-300" disabled={!!busy} onClick={() => deletePod(p)}><Trash2 /></IconButton>}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}

          {tab === 'network' && (
            <div>
              <p className="border-b border-ink-700 px-4 py-2.5 text-2xs font-medium uppercase tracking-wider text-ink-400">Domaines (Ingress)</p>
              {ingresses.length === 0 ? <Empty title="Aucun Ingress" /> : (
                <table className="w-full">
                  <thead className="border-b border-ink-700"><tr><th className="th">Domaine</th><th className="th">HTTPS</th><th className="th hidden md:table-cell">Routes</th><th className="th">Namespace</th></tr></thead>
                  <tbody className="divide-y divide-ink-700/70">
                    {ingresses.map((i) => (
                      <tr key={`${i.namespace}/${i.name}/${i.host}`}>
                        <td className="td">{i.host === '*' ? '*' : <a href={`${i.tls ? 'https' : 'http'}://${i.host}`} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1.5 text-ink-100 hover:text-brand-300">{i.host}<ExternalLink className="h-3 w-3 text-ink-500" /></a>}</td>
                        <td className="td">{i.tls ? <Badge color="green">TLS</Badge> : <Badge color="yellow">HTTP</Badge>}</td>
                        <td className="td hidden font-mono text-xs text-ink-400 md:table-cell">{i.backends.join(' · ')}</td>
                        <td className="td text-xs text-ink-400">{i.namespace}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <p className="border-y border-ink-700 px-4 py-2.5 text-2xs font-medium uppercase tracking-wider text-ink-400">Services</p>
              <table className="w-full">
                <thead className="border-b border-ink-700"><tr><th className="th">Service</th><th className="th">Type</th><th className="th hidden md:table-cell">Cluster IP</th><th className="th">Ports</th></tr></thead>
                <tbody className="divide-y divide-ink-700/70">
                  {services.map((s) => (
                    <tr key={`${s.namespace}/${s.name}`}>
                      <td className="td"><p className="font-medium">{s.name}</p><p className="text-xs text-ink-400">{s.namespace}</p></td>
                      <td className="td text-xs text-ink-300">{s.type}</td>
                      <td className="td hidden font-mono text-xs text-ink-400 md:table-cell">{s.clusterIP}</td>
                      <td className="td font-mono text-xs text-ink-300">{s.ports.join(', ')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {tab === 'events' && (events.length === 0 ? <Empty title="Aucune alerte récente" /> : (
            <table className="w-full">
              <thead className="border-b border-ink-700"><tr><th className="th">Quand</th><th className="th">Objet</th><th className="th">Raison</th><th className="th">Message</th></tr></thead>
              <tbody className="divide-y divide-ink-700/70">
                {events.map((e, i) => (
                  <tr key={i}>
                    <td className="td whitespace-nowrap text-xs text-ink-400">{ago(e.last)}{e.count > 1 && <span className="ml-1 text-ink-500">×{e.count}</span>}</td>
                    <td className="td"><p className="font-mono text-xs text-ink-100">{e.object}</p><p className="text-xs text-ink-400">{e.namespace}</p></td>
                    <td className="td"><Badge color="red">{e.reason}</Badge></td>
                    <td className="td text-xs text-ink-300">{e.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}
        </div>
      </section>
      {logs && <LogViewer target={logs} onClose={() => setLogs(null)} />}
    </>
  );
}
