/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      fontFamily: {
        sans: ['"Inter Variable"', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'monospace'],
      },
      colors: {
        ink: { 950: '#07080d', 900: '#0b0d14', 850: '#0f121b', 800: '#131724', 700: '#1b2030', 600: '#262c40', 500: '#3a4260', 400: '#646d8c', 300: '#959cb8', 200: '#c3c8dc', 100: '#e7e9f2' },
        brand: { 300: '#c4b5fd', 400: '#a78bfa', 500: '#8b5cf6', 600: '#7c3aed', 700: '#6d28d9' },
        sky2: { 400: '#60a5fa', 500: '#3b82f6' },
      },
      boxShadow: { glow: '0 0 0 1px rgba(139,92,246,.35), 0 8px 30px -8px rgba(139,92,246,.45)' },
      keyframes: { 'fade-in': { from: { opacity: 0, transform: 'translateY(4px)' }, to: { opacity: 1, transform: 'none' } } },
      animation: { 'fade-in': 'fade-in .18s ease-out' },
    },
  },
  plugins: [],
};
