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
        // neutres graphite, légèrement froids
        ink: {
          950: '#08090b', 900: '#0d0e11', 850: '#121317', 800: '#17181d', 750: '#1c1d23', 700: '#25272e',
          600: '#30323b', 500: '#4b4e5a', 400: '#7c7f8c', 300: '#a5a8b3', 200: '#cfd1d8', 100: '#eceef2',
        },
        // accent unique, tiré du logo
        brand: {
          50: '#f2f0ff', 100: '#e6e3ff', 200: '#cdc7fe', 300: '#aba1fb', 400: '#8f82f7',
          500: '#7a6bf1', 600: '#6656e3', 700: '#5546c4', 800: '#43389b',
        },
        sky2: { 400: '#60a5fa', 500: '#3b82f6' },
      },
      fontSize: { '2xs': ['11px', '16px'] },
      keyframes: { 'fade-in': { from: { opacity: 0 }, to: { opacity: 1 } } },
      animation: { 'fade-in': 'fade-in .12s ease-out' },
    },
  },
  plugins: [],
};
