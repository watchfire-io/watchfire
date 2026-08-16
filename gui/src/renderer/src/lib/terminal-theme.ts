import type { ITheme } from '@xterm/xterm'

export const terminalTheme: ITheme = {
  background: '#16181d',
  foreground: '#ffffff',
  cursor: '#e07040',
  selectionBackground: '#2d3140',
  black: '#16181d',
  red: '#ff5f57',
  green: '#28c940',
  yellow: '#ffbd2e',
  blue: '#007aff',
  magenta: '#e07040',
  cyan: '#5ac8fa',
  white: '#ffffff',
  brightBlack: '#2d3140',
  brightRed: '#ff6b6b',
  brightGreen: '#5bd75b',
  brightYellow: '#ffca4b',
  brightBlue: '#409cff',
  brightMagenta: '#e88050',
  brightCyan: '#70d7ef',
  brightWhite: '#ffffff'
}

// 'Symbols Nerd Font Mono' is bundled (public/fonts/, @font-face in
// global.css) as the guaranteed last-resort source for Nerd Font icon
// glyphs (#50): xterm's DOM renderer relies on CSS font fallback, and
// Chromium never falls back to system fonts for Private Use Area
// codepoints — so with none of the named fonts installed, icons became
// tofu boxes. Text glyphs still resolve from the earlier fonts; only
// codepoints missing from all of them reach the symbols font.
export const terminalFontFamily =
  "'MesloLGS NF', 'JetBrainsMono Nerd Font', 'Hack Nerd Font', 'FiraCode Nerd Font', 'JetBrains Mono', 'Fira Code', 'Symbols Nerd Font Mono', monospace"
