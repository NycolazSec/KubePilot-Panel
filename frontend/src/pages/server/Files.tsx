import { json } from '@codemirror/lang-json';
import { javascript } from '@codemirror/lang-javascript';
import { python } from '@codemirror/lang-python';
import { yaml } from '@codemirror/lang-yaml';
import { StreamLanguage } from '@codemirror/language';
import { properties } from '@codemirror/legacy-modes/mode/properties';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import { toml } from '@codemirror/legacy-modes/mode/toml';
import { oneDark } from '@codemirror/theme-one-dark';
import CodeMirror from '@uiw/react-codemirror';
import clsx from 'clsx';
import { Archive, ChevronRight, Download, File, FileArchive, FilePlus, FileText, Folder, FolderPlus, House, Link, PackageOpen, Pencil, RefreshCw, Save, Trash2, Upload } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { api, get, FileEntry, post } from '../../api';
import { Button, Empty, ErrorBox, IconButton, Loading, Modal, useLoad, useUI } from '../../components/ui';
import { ago, bytes, joinPath } from '../../lib/format';
import { useServer } from './ServerLayout';

const archiveRe = /\.(zip|tar|tar\.gz|tgz)$/i;
const textRe = /\.(txt|log|json|ya?ml|properties|toml|ini|cfg|conf|env|js|mjs|cjs|ts|py|sh|md|xml|html|css|csv|lang|mcmeta|sk|lua|gitignore|dockerfile)$|^[^.]+$/i;

function langFor(name: string) {
  const n = name.toLowerCase();
  if (n.endsWith('.json') || n.endsWith('.mcmeta')) return [json()];
  if (/\.(m|c)?js$/.test(n)) return [javascript()];
  if (n.endsWith('.ts')) return [javascript({ typescript: true })];
  if (n.endsWith('.py')) return [python()];
  if (/\.ya?ml$/.test(n)) return [yaml()];
  if (n.endsWith('.toml')) return [StreamLanguage.define(toml)];
  if (n.endsWith('.sh')) return [StreamLanguage.define(shell)];
  if (/\.(properties|ini|cfg|conf|env|lang)$/.test(n)) return [StreamLanguage.define(properties)];
  return [];
}

function Editor({ path, onClose }: { path: string; onClose: (saved: boolean) => void }) {
  const { server } = useServer();
  const { toast, confirm } = useUI();
  const [content, setContent] = useState<string | null>(null);
  const [orig, setOrig] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const isNew = path.startsWith('new:');
  const realPath = isNew ? path.slice(4) : path;
  const name = realPath.split('/').pop() || '';

  useEffect(() => {
    if (isNew) { setContent(''); return; }
    api<string>(`/servers/${server.id}/files/content?path=${encodeURIComponent(realPath)}`, { text: true })
      .then((t) => { setContent(t); setOrig(t); })
      .catch((e) => setError(e.message));
  }, [server.id, realPath, isNew]);

  const save = async () => {
    if (content === null) return;
    setSaving(true);
    try {
      await api(`/servers/${server.id}/files/content?path=${encodeURIComponent(realPath)}`, { method: 'PUT', raw: content, headers: { 'Content-Type': 'text/plain' } });
      setOrig(content);
      toast(`${name} enregistré`);
    } catch (e: any) {
      toast(e.message, 'err');
    } finally {
      setSaving(false);
    }
  };

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); save(); }
    };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  });

  const close = async () => {
    if (content !== null && content !== orig && !(await confirm({ title: 'Modifications non enregistrées', body: 'Fermer sans enregistrer ?', confirm: 'Fermer', danger: true }))) return;
    onClose(orig !== '' || isNew);
  };

  return (
    <Modal open onClose={close} wide="xl" title={<span className="font-mono text-sm">{realPath}{content !== orig && <span className="ml-2 text-amber-300">●</span>}</span>}
      footer={<>
        <span className="mr-auto self-center text-xs text-ink-500">Ctrl+S pour enregistrer</span>
        <Button variant="ghost" onClick={close}>Fermer</Button>
        <Button variant="primary" icon={<Save className="h-4 w-4" />} loading={saving} onClick={save} disabled={content === null}>Enregistrer</Button>
      </>}>
      {error ? <ErrorBox error={error} /> : content === null ? <Loading /> : (
        <div className="-m-5 h-[65vh] overflow-hidden border-y border-ink-700">
          <CodeMirror value={content} height="65vh" theme={oneDark} extensions={langFor(name)} onChange={setContent} basicSetup={{ highlightActiveLine: true, foldGutter: true }} />
        </div>
      )}
    </Modal>
  );
}

export default function Files() {
  const { server } = useServer();
  const { toast, confirm, prompt } = useUI();
  const [params, setParams] = useSearchParams();
  const dir = params.get('path') || '/';
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState<string | null>(null);
  const [drag, setDrag] = useState(false);
  const [upload, setUpload] = useState<{ name: string; pct: number } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const { data, error, loading, reload } = useLoad(() => get<FileEntry[]>(`/servers/${server.id}/files/list?path=${encodeURIComponent(dir)}`), [dir, server.id]);

  useEffect(() => setSelected(new Set()), [dir]);

  const go = (p: string) => setParams(p === '/' ? {} : { path: p });
  const crumbs = useMemo(() => dir.split('/').filter(Boolean), [dir]);
  const base = `/servers/${server.id}/files`;

  const run = async (fn: () => Promise<unknown>, ok?: string) => {
    try {
      await fn();
      if (ok) toast(ok);
      reload(true);
    } catch (e: any) {
      toast(e.message, 'err');
    }
  };

  const newFolder = async () => {
    const name = await prompt('Nouveau dossier', '', 'Nom du dossier');
    if (name) run(() => post(`${base}/mkdir`, { path: joinPath(dir, name) }), 'Dossier créé');
  };
  const newFile = async () => {
    const name = await prompt('Nouveau fichier', '', 'Nom du fichier');
    if (name) setEditing('new:' + joinPath(dir, name));
  };
  const rename = async (e: FileEntry) => {
    const name = await prompt('Renommer', e.name, 'Nouveau nom (ou chemin, ex : ../autre/nom)');
    if (name && name !== e.name) {
      const to = name.startsWith('/') ? name : joinPath(dir, name);
      run(() => post(`${base}/rename`, { from: joinPath(dir, e.name), to }), 'Renommé');
    }
  };
  const remove = async (names: string[]) => {
    if (!(await confirm({ title: `Supprimer ${names.length > 1 ? `${names.length} éléments` : `« ${names[0]} »`} ?`, body: 'Cette action est définitive.', confirm: 'Supprimer', danger: true }))) return;
    run(() => post(`${base}/delete`, { paths: names.map((n) => joinPath(dir, n)) }), 'Supprimé');
    setSelected(new Set());
  };
  const compress = (names: string[]) => run(async () => {
    const r = await post<{ name: string }>(`${base}/compress`, { dir, paths: names.map((n) => joinPath(dir, n)) });
    toast(`Archive créée : ${r.name}`);
    setSelected(new Set());
  });
  const decompress = (e: FileEntry) => run(() => post(`${base}/decompress`, { path: joinPath(dir, e.name) }), 'Archive extraite');
  const download = (name: string) => {
    const a = document.createElement('a');
    a.href = `/api${base}/download?path=${encodeURIComponent(joinPath(dir, name))}`;
    a.click();
  };

  const uploadFiles = (files: FileList | File[]) => {
    const list = Array.from(files);
    if (!list.length) return;
    const fd = new FormData();
    list.forEach((f) => fd.append('files', f, f.name));
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `/api${base}/upload?path=${encodeURIComponent(dir)}`);
    xhr.setRequestHeader('X-KubePilot', '1');
    const label = list.length > 1 ? `${list.length} fichiers` : list[0].name;
    setUpload({ name: label, pct: 0 });
    xhr.upload.onprogress = (e) => e.lengthComputable && setUpload({ name: label, pct: Math.round((e.loaded / e.total) * 100) });
    xhr.onload = () => {
      setUpload(null);
      if (xhr.status >= 200 && xhr.status < 300) { toast(`${label} envoyé${list.length > 1 ? 's' : ''}`); reload(true); }
      else { try { toast(JSON.parse(xhr.responseText).error, 'err'); } catch { toast(`Erreur ${xhr.status}`, 'err'); } }
    };
    xhr.onerror = () => { setUpload(null); toast('Upload interrompu', 'err'); };
    xhr.send(fd);
  };

  const open = (e: FileEntry) => {
    if (e.dir) return go(joinPath(dir, e.name));
    if (archiveRe.test(e.name)) return;
    if (textRe.test(e.name) && e.size < 8 * 1024 * 1024) setEditing(joinPath(dir, e.name));
    else download(e.name);
  };

  const toggle = (n: string) => setSelected((s) => { const x = new Set(s); x.has(n) ? x.delete(n) : x.add(n); return x; });
  const all = data && data.length > 0 && selected.size === data.length;
  const sel = Array.from(selected);

  return (
    <div
      className={clsx('card relative overflow-hidden', drag && 'ring-2 ring-brand-500')}
      onDragOver={(e) => { e.preventDefault(); setDrag(true); }}
      onDragLeave={(e) => { if (e.currentTarget === e.target) setDrag(false); }}
      onDrop={(e) => { e.preventDefault(); setDrag(false); uploadFiles(e.dataTransfer.files); }}
    >
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-ink-700 px-4 py-3">
        <div className="flex min-w-0 flex-wrap items-center gap-1 text-sm">
          <button onClick={() => go('/')} className="flex items-center gap-1.5 rounded-lg px-2 py-1 text-ink-300 hover:bg-ink-800 hover:text-ink-100"><House className="h-4 w-4" /> data</button>
          {crumbs.map((c, i) => (
            <span key={i} className="flex items-center gap-1">
              <ChevronRight className="h-3.5 w-3.5 text-ink-500" />
              <button onClick={() => go('/' + crumbs.slice(0, i + 1).join('/'))} className="rounded-lg px-2 py-1 text-ink-300 hover:bg-ink-800 hover:text-ink-100">{c}</button>
            </span>
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {sel.length > 0 && <>
            <span className="text-xs text-ink-400">{sel.length} sélectionné(s)</span>
            <Button size="sm" icon={<Archive className="h-3.5 w-3.5" />} onClick={() => compress(sel)}>Compresser</Button>
            <Button size="sm" variant="danger" icon={<Trash2 className="h-3.5 w-3.5" />} onClick={() => remove(sel)}>Supprimer</Button>
          </>}
          <IconButton title="Actualiser" onClick={() => reload()}><RefreshCw className="h-4 w-4" /></IconButton>
          <Button size="sm" icon={<FolderPlus className="h-3.5 w-3.5" />} onClick={newFolder}>Dossier</Button>
          <Button size="sm" icon={<FilePlus className="h-3.5 w-3.5" />} onClick={newFile}>Fichier</Button>
          <Button size="sm" variant="primary" icon={<Upload className="h-3.5 w-3.5" />} onClick={() => fileInput.current?.click()}>Envoyer</Button>
          <input ref={fileInput} type="file" multiple hidden onChange={(e) => { if (e.target.files) uploadFiles(e.target.files); e.target.value = ''; }} />
        </div>
      </div>

      {upload && (
        <div className="border-b border-ink-700 bg-ink-900 px-4 py-2 text-xs text-ink-300">
          Envoi de {upload.name}… {upload.pct}%
          <div className="mt-1.5 h-1 overflow-hidden rounded bg-ink-700"><div className="h-full bg-brand-500 transition-all" style={{ width: `${upload.pct}%` }} /></div>
        </div>
      )}

      {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : !data?.length ? (
        <Empty icon={<Folder className="h-6 w-6" />} title="Dossier vide">Glisse-dépose des fichiers ici pour les envoyer.</Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-ink-700">
                <th className="th w-10"><input type="checkbox" className="accent-brand-500" checked={!!all} onChange={() => setSelected(all ? new Set() : new Set(data.map((e) => e.name)))} /></th>
                <th className="th">Nom</th>
                <th className="th hidden text-right sm:table-cell">Taille</th>
                <th className="th hidden text-right md:table-cell">Modifié</th>
                <th className="w-40 px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {dir !== '/' && (
                <tr className="cursor-pointer border-b border-ink-800 hover:bg-ink-800/50" onClick={() => go('/' + crumbs.slice(0, -1).join('/'))}>
                  <td /><td className="py-2.5 text-ink-400" colSpan={4}>..</td>
                </tr>
              )}
              {data.map((e) => (
                <tr key={e.name} className={clsx('group border-b border-ink-800 hover:bg-ink-800/50', selected.has(e.name) && 'bg-brand-500/5')}>
                  <td className="px-4 py-2"><input type="checkbox" className="accent-brand-500" checked={selected.has(e.name)} onChange={() => toggle(e.name)} /></td>
                  <td className="py-2">
                    <button onClick={() => open(e)} className="flex max-w-full items-center gap-2.5 text-left text-ink-100 hover:text-brand-300">
                      {e.dir ? <Folder className="h-4 w-4 shrink-0 text-brand-400" /> : archiveRe.test(e.name) ? <FileArchive className="h-4 w-4 shrink-0 text-amber-300" /> : textRe.test(e.name) ? <FileText className="h-4 w-4 shrink-0 text-ink-300" /> : <File className="h-4 w-4 shrink-0 text-ink-400" />}
                      <span className="truncate">{e.name}</span>
                      {e.symlink && <Link className="h-3 w-3 shrink-0 text-ink-500" />}
                    </button>
                  </td>
                  <td className="hidden whitespace-nowrap px-4 py-2 text-right font-mono text-xs text-ink-400 sm:table-cell">{e.dir ? '' : bytes(e.size)}</td>
                  <td className="hidden whitespace-nowrap px-4 py-2 text-right text-xs text-ink-400 md:table-cell" title={new Date(e.modified * 1000).toLocaleString('fr-FR')}>{ago(e.modified)}</td>
                  <td className="px-4 py-1">
                    <div className="flex justify-end gap-0.5 opacity-60 group-hover:opacity-100">
                      {archiveRe.test(e.name) && <IconButton title="Extraire" onClick={() => decompress(e)}><PackageOpen className="h-4 w-4" /></IconButton>}
                      {!e.dir && textRe.test(e.name) && <IconButton title="Éditer" onClick={() => setEditing(joinPath(dir, e.name))}><FileText className="h-4 w-4" /></IconButton>}
                      <IconButton title="Renommer / déplacer" onClick={() => rename(e)}><Pencil className="h-4 w-4" /></IconButton>
                      <IconButton title={e.dir ? 'Télécharger (zip)' : 'Télécharger'} onClick={() => download(e.name)}><Download className="h-4 w-4" /></IconButton>
                      <IconButton title="Supprimer" className="hover:!text-red-300" onClick={() => remove([e.name])}><Trash2 className="h-4 w-4" /></IconButton>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {drag && <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-ink-950/70 text-lg font-medium text-brand-200"><Upload className="mr-3 h-6 w-6" /> Dépose pour envoyer dans {dir}</div>}
      {editing && <Editor path={editing} onClose={(saved) => { setEditing(null); if (saved) reload(true); }} />}
    </div>
  );
}
