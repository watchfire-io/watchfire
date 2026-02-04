# Watchfire Brand Reference

> Quick reference for AI assistants. Full guidelines in WATCHFIRE_BRAND.md

## Identity

**Name:** Watchfire
**Tagline:** Remote control for AI coding agents
**What it does:** Desktop + mobile app to monitor, control, and manage Claude Code sessions remotely

## Colors

```css
/* Primary (dark theme) */
--fire: #e07040;      /* Primary brand, CTAs, links */
--ember: #e29020;     /* Secondary accent, hover */
--flame: #fff5e6;     /* Highlights */

/* Primary (light theme) */
--fire-light: #b85a30;  /* Darker for contrast */
--ember-light: #c07818;

/* Backgrounds (dark theme) */
--bg-dark: #16181d;   /* Primary dark background */
--bg-card: #1a1d24;   /* Cards, elevated surfaces */
--border: #2d3140;    /* Borders, dividers */

/* Backgrounds (light theme) */
--bg-light: #fdfcfa;  /* Primary light background */
--bg-card-light: #f7f5f2;
--border-light: #e5e2dc;

/* Status */
--success: #28c940;   /* Complete, running */
--warning: #ffbd2e;   /* In progress, attention */
--error: #ff5f57;     /* Error states */
```

## Typography

| Element | Font | Weight | Size |
|---------|------|--------|------|
| Logo | Syne | 800 | - |
| H1 | Outfit | 700 | 48px |
| H2 | Outfit | 600 | 36px |
| H3 | Outfit | 600 | 24px |
| Body | Outfit | 400 | 16px |
| Code | JetBrains Mono | 400 | 14px |

> **Important:** Syne is reserved for the logo wordmark only. Use Outfit for all UI headings for better readability.

**Google Fonts import:**
```html
<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
```

## Logo

- Flame icon with control bar at base
- Use `watchfire-logo-dark.svg` on dark backgrounds
- Use `watchfire-logo-light.svg` on light backgrounds
- Icon only: `watchfire-icon.svg`

## Voice

- Confident but not arrogant
- Technical but accessible
- Warm and helpful
- Direct and concise

**Do:** "Monitor your AI coding sessions from anywhere"
**Don't:** "Leverage our cutting-edge solution for AI oversight"

## UI Patterns

- **Border radius:** 8px default, 12px cards, 16px modals
- **Spacing:** 8px base unit (8, 16, 24, 32, 48, 64)
- **Shadows:** Subtle, use sparingly
- **Animations:** Subtle transitions (0.2s ease)

## Key Screens

1. **Dashboard** - Session overview with status indicators
2. **Session Detail** - Task progress, logs, actions
3. **New Task** - Mobile task creation form
4. **Notifications** - Task completion alerts
5. **PR Review** - Code diff, merge actions
