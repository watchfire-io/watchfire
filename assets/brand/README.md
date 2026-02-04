# Watchfire Brand Documentation

Files to place in your app and website repositories for consistent branding.

## Contents

| File | Purpose | Where to use |
|------|---------|--------------|
| `WATCHFIRE_BRAND.md` | Full brand guidelines | Root of repo, reference in claude.md |
| `BRAND_REFERENCE.md` | Quick reference for AI | Include in claude.md context |
| `watchfire-tokens.css` | CSS variables & base styles | Import in your CSS |
| `tailwind.config.js` | Tailwind theme extension | Merge with your Tailwind config |

## Setup

### For claude.md / CLAUDE.md

Add this to your `claude.md` file:

```markdown
## Brand Guidelines

See [WATCHFIRE_BRAND.md](./WATCHFIRE_BRAND.md) for complete brand guidelines.

Quick reference:
- Primary color: #ff6b35 (Fire Orange)
- Secondary: #ffb347 (Ember Gold)  
- Background: #0a0a0b (Charcoal)
- Fonts: Syne (headings), Outfit (body), JetBrains Mono (code)

When creating UI:
- Use 8px spacing units
- Border radius: 8px default, 12px for cards
- Dark theme by default
- Subtle animations (0.2s ease)
```

### For CSS Projects

```css
@import './watchfire-tokens.css';
```

### For Tailwind Projects

```js
// tailwind.config.js
const watchfireConfig = require('./watchfire.tailwind.config.js');

module.exports = {
  // ... your config
  theme: {
    extend: {
      ...watchfireConfig.theme.extend,
    },
  },
}
```

## Google Fonts

Add to your HTML `<head>`:

```html
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Syne:wght@700;800&family=Outfit:wght@400;500;600&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
```
