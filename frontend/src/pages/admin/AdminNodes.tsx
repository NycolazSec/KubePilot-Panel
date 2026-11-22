import { Check, Copy, Cpu, HardDrive, MemoryStick, Plus, Save, Server as ServerIcon, TriangleAlert } from 'lucide-react';
import { useEffect, useState } from 'react';
import { get, NodesInfo, NodeView, post, put } from '../../api';
import { Badge, Bar, Button, Card, ErrorBox, Field, Loading, Modal, Toggle, useLoad, useUI } from '../../components/ui';
import { bytes, cpuLabel, mb } from '../../lib/format';
import { t, tn } from '../../i18n';

/** Admin → Nodes: the machines of the cluster, what runs on each, and how to add one. */
export default function AdminNodes() {
  const { data, error, loading, reload } = useLoad(() => get<NodesInfo>('/admin/nodes'));
  const [adding, setAdding] = useState(false);
  useEffect(() => {
    const id = setInterval(() => reload(true), 10000);
    return () => clearInterval(id);
  }, [reload]);
  if (error) return <ErrorBox error={error} />;
  if (loading || !data) return <Loading />;
  const several = data.nodes.length > 1;
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-3xl text-[13px] text-ink-400">{several ? t('nodes.intro') : t('nodes.introSingle')}</p>
        <Button variant="primary" size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setAdding(true)}>{t('nodes.add')}</Button>
      </div>
      {!data.agents && several && (
        <div className="flex items-start gap-2.5 rounded-md border border-amber-500/30 bg-amber-500/10 px-3.5 py-2.5 text-[13px] text-amber-200">
          <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" />{t('nodes.agentsOff')}
        </div>
      )}
      <div className="grid gap-4 xl:grid-cols-2">
        {data.nodes.map((n) => <NodeCard key={n.name} node={n} several={several} onSaved={() => reload(true)} />)}
      </div>
      <AddNodeModal open={adding} onClose={() => setAdding(false)} known={data.nodes.map((n) => n.name)} onJoined={() => reload(true)} />
    </div>
  );
}

function NodeCard({ node: n, several, onSaved }: { node: NodeView; several: boolean; onSaved: () => void }) {
  const { toast } = useUI();
  const [host, setHost] = useState(n.publicHost);
  const [accept, setAccept] = useState(n.acceptNew);
  const [busy, setBusy] = useState(false);
  const dirty = host !== n.publicHost || accept !== n.acceptNew;
  const save = async () => {
    setBusy(true);
    try { await put(`/admin/nodes/${encodeURIComponent(n.name)}`, { publicHost: host, acceptNew: accept }); toast(t('nodes.saved')); onSaved(); }
    catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const memMB = n.memCap / 1048576;
  return (
    <Card title={
      <span className="flex flex-wrap items-center gap-2">
        <ServerIcon className="h-4 w-4 text-ink-400" /><span className="font-mono">{n.name}</span>
        {n.panel && <Badge color="violet">{t('nodes.main')}</Badge>}
        {!n.ready ? <Badge color="red">{t('overview.notReady')}</Badge> : n.cordoned ? <Badge color="yellow">{t('nodes.cordoned')}</Badge> : <Badge color="green">{t('overview.ready')}</Badge>}
        {several && !n.panel && (n.agent ? <Badge color="blue">{t('nodes.agentOk')}</Badge> : <Badge color="red">{t('nodes.agentMissing')}</Badge>)}
        {several && !n.acceptNew && <Badge>{t('nodes.closed')}</Badge>}
      </span>
    }>
      <div className="space-y-4">
        <div className="grid gap-x-6 gap-y-1 text-xs text-ink-400 sm:grid-cols-2">
          <div>{t('nodes.players')} <span className="font-mono text-ink-200">{n.address || '—'}</span></div>
          <div>IP <span className="font-mono text-ink-300">{[n.externalIP, n.internalIP].filter(Boolean).join(' · ') || '—'}</span></div>
          <div className="truncate">{n.os} · {n.arch}</div>
          <div className="font-mono">{n.version}{n.controlPlane ? ` · ${t('nodes.controlPlane')}` : ''}</div>
        </div>
        {n.ready && (
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <div className="flex items-center gap-1.5 text-2xs font-medium uppercase tracking-wider text-ink-400"><Cpu className="h-3.5 w-3.5" />CPU</div>
              <div className="mt-1 text-xs tabular-nums">{cpuLabel(Math.round(n.cpu))} / {cpuLabel(n.cpuCap)}</div>
              <Bar className="mt-1.5" value={n.cpu} max={n.cpuCap} />
            </div>
            <div>
              <div className="flex items-center gap-1.5 text-2xs font-medium uppercase tracking-wider text-ink-400"><MemoryStick className="h-3.5 w-3.5" />{t('common.memory')}</div>
              <div className="mt-1 text-xs tabular-nums">{bytes(n.mem)} / {bytes(n.memCap, 0)}</div>
              <Bar className="mt-1.5" value={n.mem} max={n.memCap} />
            </div>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1 rounded-md bg-ink-800/60 px-3 py-2 text-xs text-ink-300">
          <span className="flex items-center gap-1.5"><ServerIcon className="h-3.5 w-3.5 text-ink-400" />{tn('nodes.servers', n.servers)}</span>
          <span className="flex items-center gap-1.5"><MemoryStick className="h-3.5 w-3.5 text-ink-400" />{t('nodes.reserved', { used: mb(n.memoryAllocated), total: mb(Math.round(memMB)) })}</span>
          <span className="flex items-center gap-1.5"><HardDrive className="h-3.5 w-3.5 text-ink-400" />{mb(n.diskAllocated)}</span>
        </div>
        {several && n.problem && (
          <p className="flex items-start gap-2 text-xs text-amber-300"><TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />{n.problem}</p>
        )}
        <div className="border-t border-ink-700 pt-4">
          <Field label={t('nodes.publicHost')} hint={n.panel ? t('nodes.publicHostMainHint') : t('nodes.publicHostHint')}>
            <div className="flex gap-2">
              <input className="input min-w-0 flex-1 font-mono text-xs" value={host} placeholder={n.panel ? t('nodes.publicHostMainPlaceholder') : (n.externalIP || n.internalIP)} onChange={(e) => setHost(e.target.value)} />
              <Button variant={dirty ? 'primary' : 'secondary'} icon={<Save className="h-3.5 w-3.5" />} loading={busy} disabled={!dirty} onClick={save}>{t('common.save')}</Button>
            </div>
          </Field>
        </div>
        {several && <Toggle checked={accept} onChange={setAccept} label={t('nodes.acceptNew')} />}
      </div>
    </Card>
  );
}

function Command({ cmd }: { cmd: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(cmd); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch { /* the text stays selectable */ }
  };
  return (
    <div className="flex items-start gap-2 rounded-md border border-ink-700 bg-ink-950 p-2.5">
      <code className="min-w-0 flex-1 select-all break-all font-mono text-xs text-ink-100">{cmd}</code>
      <button type="button" onClick={copy} title={t('nodes.copy')} className="shrink-0 rounded p-1 text-ink-400 hover:bg-ink-800 hover:text-ink-100">
        {copied ? <Check className="h-3.5 w-3.5 text-green-400" /> : <Copy className="h-3.5 w-3.5" />}
      </button>
    </div>
  );
}

type Invitation = { command: string; server: string; k3sVersion: string; expires: number };

function AddNodeModal({ open, onClose, known, onJoined }: { open: boolean; onClose: () => void; known: string[]; onJoined: () => void }) {
  const [address, setAddress] = useState('');
  const [inv, setInv] = useState<Invitation | null>(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [joined, setJoined] = useState<NodeView | null>(null);
  const [before, setBefore] = useState<string[]>([]);

  useEffect(() => { if (open) { setInv(null); setErr(''); setJoined(null); setBefore(known); } }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  // waiting for the new machine: it shows up in the node list, then its agent starts
  useEffect(() => {
    if (!open || !inv || (joined && joined.agent)) return;
    const id = setInterval(async () => {
      try {
        const r = await get<NodesInfo>('/admin/nodes');
        const n = r.nodes.find((x) => !before.includes(x.name));
        if (n) { setJoined(n); onJoined(); }
      } catch { /* try again */ }
    }, 3000);
    return () => clearInterval(id);
  }, [open, inv, joined, before, onJoined]);

  const create = async () => {
    setBusy(true); setErr('');
    try { setInv(await post<Invitation>('/admin/nodes/invite', address ? { address } : {})); } catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  };

  return (
    <Modal open={open} onClose={onClose} wide title={t('nodes.addTitle')} footer={<Button onClick={onClose}>{joined?.agent ? t('nodes.done') : t('common.close')}</Button>}>
      {!inv ? (
        <div className="space-y-4 text-sm">
          <p className="text-ink-300">{t('nodes.inviteIntro')}</p>
          <ul className="list-disc space-y-1 pl-5 text-[13px] text-ink-400">
            <li>{t('nodes.need1')}</li>
            <li>{t('nodes.need2')}</li>
          </ul>
          <details className="text-xs text-ink-400">
            <summary className="cursor-pointer select-none">{t('nodes.advanced')}</summary>
            <div className="mt-3"><Field label={t('nodes.mainAddress')} hint={t('nodes.mainAddressHint')}>
              <input className="input font-mono text-xs" value={address} onChange={(e) => setAddress(e.target.value.trim())} placeholder="51.68.227.100" />
            </Field></div>
          </details>
          {err && <ErrorBox error={err} />}
          <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />} loading={busy} onClick={create}>{t('nodes.createInvite')}</Button>
        </div>
      ) : (
        <div className="space-y-5 text-sm">
          <div>
            <p className="mb-2 font-medium text-ink-100">{t('nodes.pasteThis')}</p>
            <Command cmd={inv.command} />
            <p className="mt-1.5 text-xs text-ink-400">{t('nodes.inviteFacts', { version: inv.k3sVersion, time: new Date(inv.expires * 1000).toLocaleString() })}</p>
          </div>
          <div className="rounded-md border border-ink-700 bg-ink-800/50 px-3.5 py-3">
            {!joined ? (
              <p className="flex items-center gap-2.5 text-ink-300"><span className="inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-ink-500 border-t-brand-400" />{t('nodes.waiting')}</p>
            ) : !joined.agent ? (
              <p className="flex items-center gap-2.5 text-ink-200"><span className="inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-ink-500 border-t-brand-400" />{t('nodes.joinedStarting', { name: joined.name })}</p>
            ) : (
              <p className="flex items-center gap-2.5 font-medium text-green-300"><Check className="h-4 w-4" />{t('nodes.joinedReady', { name: joined.name })}</p>
            )}
          </div>
          <details className="text-xs text-ink-400">
            <summary className="cursor-pointer select-none text-ink-300">{t('nodes.trouble')}</summary>
            <ul className="mt-2 list-disc space-y-2 pl-5">
              <li>{t('nodes.troubleFirewall')}<div className="mt-1.5"><Command cmd="sudo ufw allow 6443/tcp && sudo ufw allow 10250/tcp && sudo ufw allow 51820/udp" /></div></li>
              <li>{t('nodes.troubleProvider')}</li>
              <li>{t('nodes.troubleName')}</li>
              <li>{t('nodes.troubleLog')}</li>
            </ul>
          </details>
        </div>
      )}
    </Modal>
  );
}
