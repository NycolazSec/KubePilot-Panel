import clsx from 'clsx';
import { Blocks, Container, Hexagon, Pickaxe, Terminal } from 'lucide-react';

const icons: Record<string, typeof Pickaxe> = {
  'minecraft-java': Pickaxe,
  'minecraft-bedrock': Blocks,
  nodejs: Hexagon,
  python: Terminal,
};

export default function TemplateIcon({ slug, size = 'md' }: { slug: string; size?: 'sm' | 'md' | 'lg' }) {
  const Icon = icons[slug] || (slug.startsWith('minecraft') ? Pickaxe : Container);
  const box = size === 'lg' ? 'h-11 w-11' : size === 'sm' ? 'h-7 w-7' : 'h-9 w-9';
  const ic = size === 'lg' ? 'h-5 w-5' : size === 'sm' ? 'h-3.5 w-3.5' : 'h-4 w-4';
  return (
    <div className={clsx('flex shrink-0 items-center justify-center rounded-md border border-ink-600 bg-ink-800 text-ink-200', box)}>
      <Icon className={ic} strokeWidth={1.75} />
    </div>
  );
}
