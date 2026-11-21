import { Boxes, ExternalLink, Pencil, Plus, Search, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { del, get, NodesInfo, NodeView, post, put, Server, sortTemplates, Template, User } from '../../api';
import { Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, StatusPill, Toggle, useLoad, useUI } from '../../components/ui';
import { bytes, cpuLabel, mb } from '../../lib/format';
import TemplateIcon from '../../components/TemplateIcon';
import { t, tx } from '../../i18n';

type Form = {
  id?: string; name: string; description: string; ownerId: number; templateId: number; image: string; startup?: string;
  memory: number; cpu: number; disk: number; backupLimit: number; databaseLimit: number; allocationLimit: number; suspended: boolean;
  env: Record<string, string>; startAfterInstall: boolean; node: string;
};

function ServerForm({ form, setForm, users, templates, nodes }: { form: Form; setForm: (f: Form) => void; users: User[]; templates: Template[]; nodes: NodeView[] }) {
  const tpl = templates.find((x) => x.id === form.templateId);
  const set = (k: keyof Form, v: any) => setForm({ ...form, [k]: v });
  const pickTemplate = (id: number) => {
    const nt = templates.find((x) => x.id === id);
    if (!nt) return;
    setForm({ ...form, templateId: id, image: nt.images[0]?.image || '', memory: nt.defaultMemory, cpu: nt.defaultCpu, disk: nt.defaultDisk, env: {} });
  };
  return (
    <div className="space-y-5">
      {!form.id && (
        <div>
          <span className="label">{t('adminServers.type')}</span>
          <div className="grid gap-2 sm:grid-cols-2">
            {templates.map((x) => (
              <button key={x.id} type="button" onClick={() => pickTemplate(x.id)}
                className={`flex items-start gap-3 rounded-md border p-3 text-left transition ${form.templateId === x.id ? 'border-brand-500 bg-ink-800 ring-1 ring-brand-500' : 'border-ink-700 hover:border-ink-500'}`}>
                <TemplateIcon slug={x.slug} size="sm" />
                <span className="min-w-0"><span className="block text-sm font-medium">{x.name}</span><span className="line-clamp-2 text-xs text-ink-400">{x.description}</span></span>
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t('common.name')}><input className="input" value={form.name} onChange={(e) => set('name', e.target.value)} placeholder={t('adminServers.namePlaceholder')} /></Field>
        <Field label={t('adminServers.owner')}>
          <select className="input" value={form.ownerId} onChange={(e) => set('ownerId', +e.target.value)}>
            {users.map((u) => <option key={u.id} value={u.id}>{u.username} ({u.email})</option>)}
          </select>
        </Field>
      </div>
      <Field label={t('adminServers.descOptional')}><input className="input" value={form.description} onChange={(e) => set('description', e.target.value)} /></Field>
      {!form.id && nodes.length > 1 && (
        <Field label={t('adminServers.node')} hint={t('adminServers.nodeHint')}>
          <select className="input" value={form.node} onChange={(e) => set('node', e.target.value)}>
            <option value="">{t('adminServers.nodeAuto')}</option>
            {nodes.map((n) => (
              <option key={n.name} value={n.name} disabled={!!n.problem}>
                {n.name} — {t('adminServers.nodeFree', { free: mb(Math.max(0, Math.round(n.memCap / 1048576) - n.memoryAllocated)) })}{n.problem ? ` (${n.problem})` : ''}
              </option>
            ))}
          </select>
        </Field>
      )}
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label={t('adminServers.memoryMb')} hint={mb(form.memory || 0)}><input type="number" className="input" min={128} step={256} value={form.memory} onChange={(e) => set('memory', +e.target.value)} /></Field>
        <Field label={t('adminServers.cpuMilli')} hint={t('adminServers.cpuHint', { cpu: cpuLabel(form.cpu || 0) })}><input type="number" className="input" min={0} step={250} value={form.cpu} onChange={(e) => set('cpu', +e.target.value)} /></Field>
        <Field label={t('adminServers.diskMb')} hint={mb(form.disk || 0)}><input type="number" className="input" min={256} step={1024} value={form.disk} onChange={(e) => set('disk', +e.target.value)} /></Field>
        <Field label={t('settings.maxBackups')}><input type="number" className="input" min={0} max={100} value={form.backupLimit} onChange={(e) => set('backupLimit', +e.target.value)} /></Field>
        <Field label={t('settings.maxDatabases')}><input type="number" className="input" min={0} max={50} value={form.databaseLimit} onChange={(e) => set('databaseLimit', +e.target.value)} /></Field>
        <Field label={t('settings.maxPorts')}><input type="number" className="input" min={0} max={50} value={form.allocationLimit} onChange={(e) => set('allocationLimit', +e.target.value)} /></Field>
        <Field label={t('startup.image')}>
          <input className="input font-mono text-xs" list="tpl-images" value={form.image} onChange={(e) => set('image', e.target.value)} />
          <datalist id="tpl-images">{tpl?.images.map((i) => <option key={i.image} value={i.image}>{i.name}</option>)}</datalist>
        </Field>
      </div>
      {!form.id && tpl && tpl.vars.filter((v) => v.userViewable).length > 0 && (
        <details className="rounded-md border border-ink-700 p-3">
          <summary className="cursor-pointer text-sm text-ink-300">{t('adminServers.templateVars', { count: tpl.vars.filter((v) => v.userViewable).length })}</summary>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            {tpl.vars.filter((v) => v.userViewable).map((v) => (
              <Field key={v.key} label={v.name}>
                {v.options?.length ? (
                  <select className="input" value={form.env[v.key] ?? v.default} onChange={(e) => set('env', { ...form.env, [v.key]: e.target.value })}>
                    {v.options.map((o) => <option key={o}>{o}</option>)}
                  </select>
                ) : <input className="input" value={form.env[v.key] ?? v.default} onChange={(e) => set('env', { ...form.env, [v.key]: e.target.value })} />}
              </Field>
            ))}
          </div>
        </details>
      )}
      {form.id ? (
        <>
          <Field label={t('startup.command')} hint={t('adminServers.startupHint')}>
            <textarea className="input min-h-[90px] font-mono text-xs" value={form.startup || ''} onChange={(e) => set('startup', e.target.value)} spellCheck={false} />
          </Field>
          <Toggle checked={form.suspended} onChange={(v) => set('suspended', v)} label={t('adminServers.suspended')} />
        </>
      ) : (
        <Toggle checked={form.startAfterInstall} onChange={(v) => set('startAfterInstall', v)} label={t('adminServers.autoStart')} />
      )}
    </div>
  );
}

export default function AdminServers() {
  const { toast, confirm } = useUI();
  const servers = useLoad(() => get<Server[]>('/servers?all=1'));
  const users = useLoad(() => get<User[]>('/admin/users'));
  const templates = useLoad(() => get<Template[]>('/admin/templates').then(sortTemplates));
  const nodes = useLoad(() => get<NodesInfo>('/admin/nodes').then((r) => r.nodes).catch(() => [] as NodeView[]));
  const several = (nodes.data?.length ?? 0) > 1;
  const [form, setForm] = useState<Form | null>(null);
  const [busy, setBusy] = useState(false);
  const [q, setQ] = useState('');

  const list = useMemo(() => (servers.data || []).filter((s) => `${s.name} ${s.owner} ${s.template} ${s.id}`.toLowerCase().includes(q.toLowerCase())), [servers.data, q]);

  const openCreate = () => {
    const first = templates.data?.[0];
    if (!first || !users.data?.length) return;
    setForm({ name: '', description: '', ownerId: users.data[0].id, templateId: first.id, image: first.images[0]?.image, memory: first.defaultMemory, cpu: first.defaultCpu, disk: first.defaultDisk, backupLimit: 3, databaseLimit: 1, allocationLimit: 1, suspended: false, env: {}, startAfterInstall: true, node: '' });
  };
  const openEdit = (s: Server) => setForm({ id: s.id, name: s.name, description: s.description, ownerId: s.ownerId, templateId: s.templateId, image: s.image, startup: s.startup, memory: s.memory, cpu: s.cpu, disk: s.disk, backupLimit: s.backupLimit, databaseLimit: s.databaseLimit, allocationLimit: s.allocationLimit, suspended: s.suspended, env: s.env, startAfterInstall: false, node: s.node || '' });

  const save = async () => {
    if (!form) return;
    setBusy(true);
    try {
      if (form.id) { await put(`/admin/servers/${form.id}`, form); toast(t('adminServers.updated')); }
      else { await post('/admin/servers', form); toast(t('adminServers.created')); }
      setForm(null);
      servers.reload(true);
      nodes.reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };

  const remove = async (s: Server) => {
    const ok = await confirm({ title: t('files.deleteOne', { name: s.name }), danger: true, confirm: t('adminServers.deleteForever'),
      body: tx('adminServers.deleteBody', { allFiles: <b>{t('adminServers.allFiles')}</b> }) });
    if (!ok) return;
    try { await del(`/admin/servers/${s.id}`); toast(t('adminServers.deleted')); servers.reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  const err = servers.error || users.error || templates.error;
  return (
    <>
      <Card padded={false} title={t('adminServers.title', { count: servers.data?.length ?? '…' })}
        actions={<>
          <div className="relative hidden sm:block"><Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-400" /><input className="input !w-56 !py-1.5 pl-8 text-xs" placeholder={t('adminServers.filter')} value={q} onChange={(e) => setQ(e.target.value)} /></div>
          <Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={openCreate} disabled={!templates.data || !users.data}>{t('adminServers.create')}</Button>
        </>}>
        {err ? <div className="p-4"><ErrorBox error={err} /></div> : servers.loading ? <Loading /> : !list.length ? (
          <Empty icon={<Boxes className="h-6 w-6" />} title={t('dashboard.noServers')}>{t('adminServers.emptyHint')}</Empty>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b border-ink-700">
                <th className="th">{t('adminServers.server')}</th><th className="th">{t('adminServers.owner')}</th><th className="th">{t('overview.state')}</th><th className="th">{t('adminServers.resources')}</th>{several && <th className="th">{t('adminServers.node')}</th>}<th className="th">{t('common.address')}</th><th className="w-32" />
              </tr></thead>
              <tbody>
                {list.map((s) => (
                  <tr key={s.id} className="border-b border-ink-800 hover:bg-ink-800/40">
                    <td className="px-4 py-3"><div className="flex items-center gap-2.5"><TemplateIcon slug={s.templateSlug} size="sm" /><div><div className="font-medium">{s.name}</div><div className="font-mono text-xs text-ink-500">#{s.id} · {s.template}</div></div></div></td>
                    <td className="px-4 py-3 text-ink-300">{s.owner}</td>
                    <td className="px-4 py-3">{s.suspended ? <span className="text-xs text-red-300">{t('server.suspended')}</span> : <StatusPill status={s.state.status} />}</td>
                    <td className="px-4 py-3 text-xs text-ink-400">{mb(s.memory)} · {cpuLabel(s.cpu)} · {bytes(s.diskUsed, 0)} / {mb(s.disk)}</td>
                    {several && <td className="px-4 py-3 font-mono text-xs text-ink-300">{s.nodeName || '—'}</td>}
                    <td className="px-4 py-3 font-mono text-xs text-ink-300">{s.address || '—'}</td>
                    <td className="px-4 py-2"><div className="flex justify-end gap-1">
                      <Link to={`/server/${s.id}`} title={t('common.open')} className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-ink-300 hover:bg-ink-700 hover:text-ink-100"><ExternalLink className="h-4 w-4" /></Link>
                      <IconButton title={t('common.edit')} onClick={() => openEdit(s)}><Pencil className="h-4 w-4" /></IconButton>
                      <IconButton title={t('common.delete')} className="hover:!text-red-300" onClick={() => remove(s)}><Trash2 className="h-4 w-4" /></IconButton>
                    </div></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <Modal open={!!form} onClose={() => setForm(null)} wide title={form?.id ? t('adminServers.editTitle', { name: form.name }) : t('adminServers.create')}
        footer={<><Button variant="ghost" onClick={() => setForm(null)}>{t('common.cancel')}</Button><Button variant="primary" loading={busy} onClick={save}>{form?.id ? t('common.save') : t('adminServers.createInstall')}</Button></>}>
        {form && users.data && templates.data && <ServerForm form={form} setForm={setForm} users={users.data} templates={templates.data} nodes={nodes.data || []} />}
      </Modal>
    </>
  );
}
