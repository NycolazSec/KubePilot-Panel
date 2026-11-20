import { Copy, Download, Layers, Pencil, Plus, Trash2, Upload, X } from 'lucide-react';
import { useRef, useState } from 'react';
import { del, get, post, put, sortTemplates, Template, TemplateVar } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, Toggle, useLoad, useUI } from '../../components/ui';
import { cpuLabel, mb } from '../../lib/format';
import TemplateIcon from '../../components/TemplateIcon';
// `t` is used for templates in this file: the translation function is imported as `tr`
import { t as tr } from '../../i18n';

const blank: Template = {
  id: 0, slug: '', name: '', description: '', category: tr('tpl.otherCategory'), images: [{ name: tr('tpl.defaultImage'), image: '' }], startup: '', dataPath: '/home/container',
  vars: [], ports: [{ name: 'app', port: 8080, protocol: 'tcp' }], stopGrace: 30, readyProbe: false, runAsUser: 1000, consoleInput: true,
  installImage: 'busybox:1.36', installScript: '', defaultMemory: 1024, defaultCpu: 1000, defaultDisk: 5120,
};

function TemplateEditor({ t, setT }: { t: Template; setT: (t: Template) => void }) {
  const set = (k: keyof Template, v: any) => setT({ ...t, [k]: v });
  const setVar = (i: number, patch: Partial<TemplateVar>) => set('vars', t.vars.map((v, j) => (j === i ? { ...v, ...patch } : v)));
  return (
    <div className="space-y-6">
      <section className="grid gap-4 sm:grid-cols-3">
        <Field label={tr('common.name')}><input className="input" value={t.name} onChange={(e) => set('name', e.target.value)} /></Field>
        <Field label={tr('tpl.slug')}><input className="input font-mono" value={t.slug} onChange={(e) => set('slug', e.target.value.toLowerCase())} /></Field>
        <Field label={tr('tpl.category')}><input className="input" value={t.category} onChange={(e) => set('category', e.target.value)} /></Field>
        <Field label={tr('common.description')} className="sm:col-span-3"><input className="input" value={t.description} onChange={(e) => set('description', e.target.value)} /></Field>
      </section>

      <section>
        <div className="mb-2 flex items-center justify-between"><span className="label !mb-0">{tr('tpl.images')}</span>
          <Button size="sm" variant="ghost" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => set('images', [...t.images, { name: '', image: '' }])}>{tr('common.add')}</Button></div>
        <div className="space-y-2">
          {t.images.map((im, i) => (
            <div key={i} className="flex gap-2">
              <input className="input !w-1/3" placeholder={tr('tpl.label')} value={im.name} onChange={(e) => set('images', t.images.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
              <input className="input font-mono text-xs" placeholder={tr('tpl.imagePlaceholder')} value={im.image} onChange={(e) => set('images', t.images.map((x, j) => (j === i ? { ...x, image: e.target.value } : x)))} />
              <IconButton title={tr('network.remove')} onClick={() => set('images', t.images.filter((_, j) => j !== i))} disabled={t.images.length === 1}><X className="h-4 w-4" /></IconButton>
            </div>
          ))}
        </div>
      </section>

      <section className="grid gap-4 sm:grid-cols-2">
        <Field label={tr('tpl.startup')} hint={tr('tpl.startupHint')} className="sm:col-span-2">
          <textarea className="input min-h-[120px] font-mono text-xs" value={t.startup} onChange={(e) => set('startup', e.target.value)} spellCheck={false} />
        </Field>
        <Field label={tr('tpl.dataPath')}><input className="input font-mono" value={t.dataPath} onChange={(e) => set('dataPath', e.target.value)} /></Field>
        <Field label={tr('tpl.uid')} hint={tr('tpl.uidHint')}><input type="number" className="input" value={t.runAsUser} onChange={(e) => set('runAsUser', +e.target.value)} /></Field>
        <Field label={tr('tpl.stopGrace')} hint={tr('tpl.stopGraceHint')}><input type="number" className="input" value={t.stopGrace} onChange={(e) => set('stopGrace', +e.target.value)} /></Field>
        <div className="flex flex-col justify-center gap-3">
          <Toggle checked={t.consoleInput} onChange={(v) => set('consoleInput', v)} label={tr('tpl.consoleInput')} />
          <Toggle checked={t.readyProbe} onChange={(v) => set('readyProbe', v)} label={tr('tpl.readyProbe')} />
        </div>
      </section>

      <section className="grid gap-4 sm:grid-cols-3">
        <Field label={tr('tpl.defMemory')} hint={mb(t.defaultMemory)}><input type="number" className="input" value={t.defaultMemory} onChange={(e) => set('defaultMemory', +e.target.value)} /></Field>
        <Field label={tr('tpl.defCpu')} hint={cpuLabel(t.defaultCpu)}><input type="number" className="input" value={t.defaultCpu} onChange={(e) => set('defaultCpu', +e.target.value)} /></Field>
        <Field label={tr('tpl.defDisk')} hint={mb(t.defaultDisk)}><input type="number" className="input" value={t.defaultDisk} onChange={(e) => set('defaultDisk', +e.target.value)} /></Field>
      </section>

      <section>
        <div className="mb-2 flex items-center justify-between"><span className="label !mb-0">{tr('tpl.ports')}</span>
          <Button size="sm" variant="ghost" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => set('ports', [...t.ports, { name: 'port', port: 8080, protocol: 'tcp' }])}>{tr('common.add')}</Button></div>
        <div className="space-y-2">
          {t.ports.map((p, i) => (
            <div key={i} className="flex gap-2">
              <input className="input min-w-0" placeholder={tr('tpl.portName')} value={p.name} onChange={(e) => set('ports', t.ports.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
              <input type="number" className="input !w-24 shrink-0 sm:!w-32" value={p.port} onChange={(e) => set('ports', t.ports.map((x, j) => (j === i ? { ...x, port: +e.target.value } : x)))} />
              <select className="input !w-28 shrink-0 sm:!w-36" value={p.protocol} onChange={(e) => set('ports', t.ports.map((x, j) => (j === i ? { ...x, protocol: e.target.value } : x)))}>
                <option value="tcp">TCP</option><option value="udp">UDP</option><option value="tcp+udp">TCP+UDP</option>
              </select>
              <IconButton title={tr('network.remove')} onClick={() => set('ports', t.ports.filter((_, j) => j !== i))}><X className="h-4 w-4" /></IconButton>
            </div>
          ))}
        </div>
      </section>

      <section>
        <div className="mb-2 flex items-center justify-between"><span className="label !mb-0">{tr('tpl.envVars')}</span>
          <Button size="sm" variant="ghost" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => set('vars', [...t.vars, { key: tr('tpl.newVarKey'), name: tr('tpl.newVarName'), description: '', default: '', userViewable: true, userEditable: true }])}>{tr('common.add')}</Button></div>
        <div className="space-y-3">
          {t.vars.map((v, i) => (
            <div key={i} className="rounded-md border border-ink-700 p-3">
              <div className="grid gap-2 sm:grid-cols-3">
                <input className="input font-mono text-xs" placeholder={tr('tpl.key')} value={v.key} onChange={(e) => setVar(i, { key: e.target.value.toUpperCase() })} />
                <input className="input" placeholder={tr('tpl.label')} value={v.name} onChange={(e) => setVar(i, { name: e.target.value })} />
                <input className="input font-mono text-xs" placeholder={tr('tpl.defaultValue')} value={v.default} onChange={(e) => setVar(i, { default: e.target.value })} />
                <input className="input sm:col-span-3" placeholder={tr('common.description')} value={v.description} onChange={(e) => setVar(i, { description: e.target.value })} />
                <input className="input text-xs" placeholder={tr('tpl.choices')} value={(v.options || []).join(',')} onChange={(e) => setVar(i, { options: e.target.value ? e.target.value.split(',') : [] })} />
                <input className="input font-mono text-xs" placeholder={tr('tpl.regex')} value={v.pattern || ''} onChange={(e) => setVar(i, { pattern: e.target.value })} />
                <div className="flex items-center justify-between gap-2">
                  <div className="flex flex-col gap-1.5">
                    <Toggle checked={v.userViewable} onChange={(x) => setVar(i, { userViewable: x })} label={tr('tpl.visible')} />
                    <Toggle checked={v.userEditable} onChange={(x) => setVar(i, { userEditable: x })} label={tr('tpl.editable')} />
                  </div>
                  <IconButton title={tr('common.delete')} onClick={() => set('vars', t.vars.filter((_, j) => j !== i))}><Trash2 className="h-4 w-4" /></IconButton>
                </div>
              </div>
            </div>
          ))}
        </div>
        <p className="mt-2 text-xs text-ink-400">{tr('tpl.alwaysVars')}</p>
      </section>

      <section className="grid gap-4 sm:grid-cols-3">
        <Field label={tr('tpl.installImage')}><input className="input font-mono text-xs" value={t.installImage} onChange={(e) => set('installImage', e.target.value)} /></Field>
        <Field label={tr('tpl.installScript')} className="sm:col-span-2">
          <textarea className="input min-h-[90px] font-mono text-xs" value={t.installScript} onChange={(e) => set('installScript', e.target.value)} spellCheck={false} />
        </Field>
      </section>
    </div>
  );
}

export default function AdminTemplates() {
  const { toast, confirm } = useUI();
  const { data, error, loading, reload } = useLoad(() => get<Template[]>('/admin/templates').then(sortTemplates));
  const [edit, setEdit] = useState<Template | null>(null);
  const [busy, setBusy] = useState(false);
  const importRef = useRef<HTMLInputElement>(null);

  const save = async () => {
    if (!edit) return;
    setBusy(true);
    try {
      if (edit.id) await put(`/admin/templates/${edit.id}`, edit);
      else await post('/admin/templates', edit);
      toast(tr('tpl.saved'));
      setEdit(null);
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const remove = async (t: Template) => {
    if (!(await confirm({ title: tr('tpl.deleteTitle', { name: t.name }), confirm: tr('common.delete'), danger: true }))) return;
    try { await del(`/admin/templates/${t.id}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };
  const exportT = (t: Template) => {
    const { id, updatedAt, ...rest } = t;
    void id; void updatedAt;
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([JSON.stringify(rest, null, 2)], { type: 'application/json' }));
    a.download = `kubepilot-template-${t.slug}.json`;
    a.click();
  };
  const importT = async (f: File) => {
    try {
      const t = JSON.parse(await f.text());
      setEdit({ ...blank, ...t, id: 0 });
    } catch { toast(tr('tpl.invalidJson'), 'err'); }
  };

  return (
    <>
      <Card padded={false} title={tr('tpl.title')}
        actions={<>
          <Button size="sm" icon={<Upload className="h-3.5 w-3.5" />} onClick={() => importRef.current?.click()}>{tr('tpl.import')}</Button>
          <input ref={importRef} type="file" accept="application/json" hidden onChange={(e) => { if (e.target.files?.[0]) importT(e.target.files[0]); e.target.value = ''; }} />
          <Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setEdit({ ...blank })}>{tr('tpl.new')}</Button>
        </>}>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading || !data ? <Loading /> : !data.length ? <Empty icon={<Layers className="h-6 w-6" />} title={tr('tpl.empty')} /> : (
          <ul className="divide-y divide-ink-800">
            {data.map((t) => (
              <li key={t.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
                <TemplateIcon slug={t.slug} />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2"><span className="font-medium">{t.name}</span><Badge>{t.category}</Badge><code className="font-mono text-xs text-ink-500">{t.slug}</code></div>
                  <p className="mt-0.5 truncate text-xs text-ink-400">{tr('tpl.summary', { image: t.images[0]?.image ?? '', vars: t.vars.length, ports: t.ports.map((p) => `${p.port}/${p.protocol}`).join(', ') || tr('tpl.noPort') })}</p>
                </div>
                <div className="flex gap-1">
                  <IconButton title={tr('tpl.duplicate')} onClick={() => setEdit({ ...t, id: 0, slug: `${t.slug}-copy`, name: `${t.name} (${tr('tpl.copySuffix')})` })}><Copy className="h-4 w-4" /></IconButton>
                  <IconButton title={tr('tpl.export')} onClick={() => exportT(t)}><Download className="h-4 w-4" /></IconButton>
                  <IconButton title={tr('common.edit')} onClick={() => setEdit(structuredClone(t))}><Pencil className="h-4 w-4" /></IconButton>
                  <IconButton title={tr('common.delete')} className="hover:!text-red-300" onClick={() => remove(t)}><Trash2 className="h-4 w-4" /></IconButton>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <Modal open={!!edit} onClose={() => setEdit(null)} wide="xl" title={edit?.id ? tr('adminServers.editTitle', { name: edit.name }) : tr('tpl.newTitle')}
        footer={<><span className="mr-auto self-center text-xs text-ink-500">{tr('tpl.applyHint')}</span><Button variant="ghost" onClick={() => setEdit(null)}>{tr('common.cancel')}</Button><Button variant="primary" loading={busy} onClick={save}>{tr('common.save')}</Button></>}>
        {edit && <TemplateEditor t={edit} setT={setEdit} />}
      </Modal>
    </>
  );
}
