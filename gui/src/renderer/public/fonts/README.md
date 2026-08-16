# Bundled fonts

## SymbolsNerdFontMono-Regular.woff2

Symbols Nerd Font Mono (icons-only font) from the Nerd Fonts project
(https://github.com/ryanoasis/nerd-fonts), release asset
`NerdFontsSymbolsOnly.zip`, converted from TTF to WOFF2 with fonttools
(lossless container change — same glyph tables, smaller file).

Why it's bundled: the embedded xterm.js terminals use the DOM renderer, and
Chromium does NOT perform system-wide font fallback for Private Use Area
codepoints — the ones Nerd Fonts use for file-type/git/powerline icons. If
the user's particular Nerd Font isn't matched by name in `terminalFontFamily`
(`src/lib/terminal-theme.ts`), every icon renders as a tofu box (issue #50).
Appending this symbols-only font as the last resort before `monospace`
guarantees icon glyphs always resolve, whatever font the user's shell prompt
assumes, while regular text still comes from the earlier fonts in the stack.

License: MIT (see LICENSE-nerd-fonts-symbols in this directory).
