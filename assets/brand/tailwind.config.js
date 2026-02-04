// Watchfire Tailwind Configuration
// Add this to your tailwind.config.js

/** @type {import('tailwindcss').Config} */
module.exports = {
  theme: {
    extend: {
      colors: {
        // Brand colors
        fire: {
          DEFAULT: '#e07040',
          50: '#fef6f3',
          100: '#fde9e1',
          200: '#fbd3c3',
          300: '#f5b096',
          400: '#e88050',
          500: '#e07040',
          600: '#c45a30',
          700: '#a54828',
          800: '#863b22',
          900: '#6e3220',
        },
        ember: {
          DEFAULT: '#e29020',
          light: '#f0b050',
          dark: '#b87018',
        },
        flame: '#fff5e6',

        // Background colors (dark theme)
        charcoal: {
          DEFAULT: '#16181d',
          50: '#2d3140',
          100: '#22262f',
          200: '#1a1d24',
          300: '#16181d',
        },

        // Status colors
        success: '#28c940',
        warning: '#ffbd2e',
        error: '#ff5f57',
      },

      fontFamily: {
        heading: ['Outfit', 'system-ui', 'sans-serif'],
        body: ['Outfit', 'system-ui', 'sans-serif'],
        mono: ['JetBrains Mono', 'Fira Code', 'monospace'],
      },

      fontSize: {
        '4xl': ['3rem', { lineHeight: '1.1' }],
        '3xl': ['2.25rem', { lineHeight: '1.2' }],
        '2xl': ['1.5rem', { lineHeight: '1.3' }],
      },

      borderRadius: {
        'wf': '8px',
        'wf-lg': '12px',
        'wf-xl': '16px',
        'wf-2xl': '24px',
      },

      boxShadow: {
        'wf': '0 4px 12px rgba(0, 0, 0, 0.4)',
        'wf-lg': '0 8px 24px rgba(0, 0, 0, 0.4)',
        'wf-glow': '0 0 20px rgba(224, 112, 64, 0.3)',
      },

      animation: {
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
      },
    },
  },
}

/*
Usage examples:

<h1 class="font-heading text-4xl font-bold text-white">
  Watchfire
</h1>

<button class="bg-fire hover:bg-fire-400 text-white font-semibold px-6 py-3 rounded-wf transition-all">
  Get Started
</button>

<div class="bg-charcoal-200 border border-charcoal-50 rounded-wf-lg p-6 hover:border-fire transition-all">
  Card content
</div>

<span class="text-success">● Running</span>
<span class="text-warning">● In Progress</span>
<span class="text-error">● Error</span>

Note: Syne is reserved for the logo wordmark only. Use Outfit (font-heading)
for all UI headings - it provides better readability at smaller sizes.
*/
