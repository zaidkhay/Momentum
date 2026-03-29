/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        bg:        '#FFFBF1',
        surface:   '#F5F0E4',
        border:    '#E2DDD4',
        accent:    '#EDE8DC',
        primary:   '#0D0D0D',
        secondary: '#7A7772',
        muted:     '#9A9590',
        hint:      '#B0ABA3',
        up:        '#1A6B3C',
        'up-bg':   '#EAF4EE',
        down:      '#8B1A1A',
        'down-bg': '#F7EAEA',
      },
    },
  },
  plugins: [],
};
