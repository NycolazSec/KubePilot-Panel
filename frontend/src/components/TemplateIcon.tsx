import clsx from 'clsx';
import { Blocks, Container, Hexagon, Pickaxe, Terminal } from 'lucide-react';

const styles: Record<string, { icon: typeof Pickaxe; cls: string }> = {
  'minecraft-java': { icon: Pickaxe, cls: 'from-emerald-500/25 to-emerald-500/5 text-emerald-300 ring-emerald-500/30' },
  'minecraft-bedrock': { icon: Blocks, cls: 'from-lime-500/25 to-lime-500/5 text-lime-300 ring-lime-500/30' },
  nodejs: { icon: Hexagon, cls: 'from-green-500/25 to-green-500/5 text-green-300 ring-green-500/30' },
  python: { icon: Terminal, cls: 'from-sky-500/25 to-yellow-500/10 text-sky-300 ring-sky-500/30' },
};

export default function TemplateIcon({ slug, size = 'md' }: { slug: string; size?: 'sm' | 'md' | 'lg' }) {
  const s = styles[slug] || (slug.startsWith('minecraft') ? styles['minecraft-java'] : { icon: Container, cls: 'from-brand-500/25 to-brand-500/5 text-brand-300 ring-brand-500/30' });
  const Icon = s.icon;
  const box = size === 'lg' ? 'h-14 w-14 rounded-2xl' : size === 'sm' ? 'h-8 w-8 rounded-lg' : 'h-11 w-11 rounded-xl';
  const ic = size === 'lg' ? 'h-7 w-7' : size === 'sm' ? 'h-4 w-4' : 'h-5 w-5';
  return <div className={clsx('flex shrink-0 items-center justify-center bg-gradient-to-br ring-1', box, s.cls)}><Icon className={ic} /></div>;
}
