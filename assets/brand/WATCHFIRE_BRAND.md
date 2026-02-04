# Watchfire Brand Guidelines

## Brand Overview

**Watchfire** is a command center for AI coding agents. It enables developers to monitor, control, and manage Claude Code sessions remotely—from desktop and mobile.

**Tagline:** "Remote control for AI coding agents"

---

## Logo

The Watchfire logo consists of a flame icon with a subtle control bar at the base, paired with the wordmark.

### Logo Files
- `watchfire-logo-dark.svg` — Use on dark backgrounds
- `watchfire-logo-light.svg` — Use on light backgrounds
- `watchfire-icon.svg` — Icon only (app icons, favicons)
- `watchfire-favicon.svg` — Simplified for small sizes

### Logo Usage
- Minimum clear space: Height of the flame on all sides
- Minimum size: 120px wide for full logo, 32px for icon
- Never distort, rotate, or add effects to the logo
- Never change the logo colors outside of approved variants

---

## Color Palette

### Primary Colors (Dark Theme)

| Name | Hex | RGB | Usage |
|------|-----|-----|-------|
| **Fire Orange** | `#e07040` | 224, 112, 64 | Primary brand color, CTAs, links |
| **Ember Gold** | `#e29020` | 226, 144, 32 | Secondary accents, hover states |
| **Flame White** | `#fff5e6` | 255, 245, 230 | Highlights, light accents |

### Primary Colors (Light Theme)

| Name | Hex | RGB | Usage |
|------|-----|-----|-------|
| **Fire Orange** | `#b85a30` | 184, 90, 48 | Primary brand color (darker for contrast) |
| **Ember Gold** | `#c07818` | 192, 120, 24 | Secondary accents |
| **Flame White** | `#fff5e6` | 255, 245, 230 | Highlights |

### Background Colors (Dark Theme)

| Name | Hex | RGB | Usage |
|------|-----|-----|-------|
| **Charcoal** | `#16181d` | 22, 24, 29 | Primary dark background |
| **Card Gray** | `#1a1d24` | 26, 29, 36 | Cards, elevated surfaces |
| **Elevated** | `#22262f` | 34, 38, 47 | Higher elevation surfaces |
| **Border** | `#2d3140` | 45, 49, 64 | Borders, dividers |

### Background Colors (Light Theme)

| Name | Hex | RGB | Usage |
|------|-----|-----|-------|
| **Off-white** | `#fdfcfa` | 253, 252, 250 | Primary light background |
| **Card** | `#f7f5f2` | 247, 245, 242 | Cards, elevated surfaces |
| **Elevated** | `#ffffff` | 255, 255, 255 | Higher elevation surfaces |
| **Border** | `#e5e2dc` | 229, 226, 220 | Borders, dividers |

### Text Colors

| Name | Hex | Usage |
|------|-----|-------|
| **Primary Text** | `#ffffff` | Headings, body text (dark mode) |
| **Secondary Text** | `#888888` | Descriptions, labels |
| **Muted Text** | `#888888` | Captions, helper text |
| **Dark Text** | `#1a1d24` | Body text (light mode) |

### CSS Variables

```css
:root {
  /* Brand colors (dark theme) */
  --color-fire: #e07040;
  --color-ember: #e29020;
  --color-flame: #fff5e6;

  /* Backgrounds (dark theme) */
  --color-bg-primary: #16181d;
  --color-bg-secondary: #1a1d24;
  --color-bg-elevated: #22262f;

  /* Text */
  --color-text-primary: #ffffff;
  --color-text-secondary: #888888;
  --color-text-muted: #888888;

  /* Borders */
  --color-border: #2d3140;
  --color-border-hover: #e07040;
}

/* Light theme overrides */
[data-theme="light"] {
  --color-fire: #b85a30;
  --color-ember: #c07818;
  --color-bg-primary: #fdfcfa;
  --color-bg-secondary: #f7f5f2;
  --color-bg-elevated: #ffffff;
  --color-text-primary: #1a1d24;
  --color-text-secondary: #555555;
  --color-border: #e5e2dc;
}
```

---

## Typography

### Font Families

**Logo & Wordmark:** Syne
- Source: Google Fonts
- Weights: 700 (Bold), 800 (Extra Bold)
- **Reserved for logo and large branding elements only**

**Headings & UI:** Outfit
- Source: Google Fonts
- Weights: 400 (Regular), 500 (Medium), 600 (Semi Bold), 700 (Bold)
- `font-family: 'Outfit', sans-serif;`

> **Note:** We use Outfit for all UI headings instead of Syne. Syne is a display font designed for large branding applications - it has reduced readability at smaller sizes typical of UI headings. Outfit provides excellent readability while maintaining a modern, professional feel.

**Body & UI:** Outfit
- Source: Google Fonts
- Weights: 400 (Regular), 500 (Medium), 600 (Semi Bold)
- `font-family: 'Outfit', sans-serif;`

**Code & Monospace:** JetBrains Mono
- Source: Google Fonts
- Weights: 400 (Regular), 500 (Medium)
- `font-family: 'JetBrains Mono', monospace;`

### Font Import

```html
<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
```

### Type Scale

| Element | Font | Size | Weight | Line Height |
|---------|------|------|--------|-------------|
| H1 | Outfit | 48px / 3rem | 700 | 1.1 |
| H2 | Outfit | 36px / 2.25rem | 600 | 1.2 |
| H3 | Outfit | 24px / 1.5rem | 600 | 1.3 |
| H4 | Outfit | 20px / 1.25rem | 600 | 1.4 |
| Body | Outfit | 16px / 1rem | 400 | 1.6 |
| Body Large | Outfit | 18px / 1.125rem | 400 | 1.6 |
| Small | Outfit | 14px / 0.875rem | 400 | 1.5 |
| Caption | Outfit | 12px / 0.75rem | 500 | 1.4 |
| Code | JetBrains Mono | 14px / 0.875rem | 400 | 1.6 |

---

## Spacing

Use an 8px base unit for consistent spacing.

```css
--space-1: 4px;
--space-2: 8px;
--space-3: 12px;
--space-4: 16px;
--space-5: 24px;
--space-6: 32px;
--space-7: 48px;
--space-8: 64px;
--space-9: 96px;
```

---

## Border Radius

```css
--radius-sm: 4px;
--radius-md: 8px;
--radius-lg: 12px;
--radius-xl: 16px;
--radius-2xl: 24px;
--radius-full: 9999px;
```

---

## Shadows

```css
/* Dark theme */
--shadow-sm: 0 1px 2px rgba(0, 0, 0, 0.5);
--shadow-md: 0 4px 12px rgba(0, 0, 0, 0.4);
--shadow-lg: 0 8px 24px rgba(0, 0, 0, 0.4);
--shadow-glow: 0 0 20px rgba(224, 112, 64, 0.3);

/* Light theme */
--shadow-sm: 0 1px 2px rgba(0, 0, 0, 0.08);
--shadow-md: 0 4px 12px rgba(0, 0, 0, 0.1);
--shadow-lg: 0 8px 24px rgba(0, 0, 0, 0.12);
--shadow-glow: 0 0 20px rgba(184, 90, 48, 0.2);
```

---

## Components

### Buttons

**Primary Button**
```css
.btn-primary {
  background: #e07040;
  color: #ffffff;
  font-family: 'Outfit', sans-serif;
  font-weight: 600;
  padding: 12px 24px;
  border-radius: 8px;
  border: none;
  cursor: pointer;
  transition: all 0.2s ease;
}

.btn-primary:hover {
  background: #e88050;
  transform: translateY(-1px);
}
```

**Secondary Button**
```css
.btn-secondary {
  background: transparent;
  color: #ffffff;
  font-family: 'Outfit', sans-serif;
  font-weight: 500;
  padding: 12px 24px;
  border-radius: 8px;
  border: 1px solid #2d3140;
  cursor: pointer;
  transition: all 0.2s ease;
}

.btn-secondary:hover {
  border-color: #e07040;
  color: #e07040;
}
```

### Cards

```css
.card {
  background: #1a1d24;
  border: 1px solid #2d3140;
  border-radius: 12px;
  padding: 24px;
  transition: all 0.2s ease;
}

.card:hover {
  border-color: #e07040;
  transform: translateY(-2px);
}
```

### Links

```css
a {
  color: #e07040;
  text-decoration: none;
  transition: color 0.2s ease;
}

a:hover {
  color: #e29020;
}
```

---

## Iconography

- Style: Outlined, 2px stroke
- Corners: Rounded
- Recommended library: Lucide Icons
- Default size: 24px
- Colors: Match text color or use brand orange for emphasis

---

## Voice & Tone

**Personality:**
- Confident but not arrogant
- Technical but accessible
- Warm and helpful
- Direct and concise

**Writing style:**
- Use active voice
- Keep sentences short
- Avoid jargon when possible
- Be specific over vague

**Example copy:**
- ✓ "Monitor your AI coding sessions from anywhere"
- ✗ "Leverage our cutting-edge solution for AI oversight"
- ✓ "Issue tasks from your phone"
- ✗ "Utilize mobile interfaces to dispatch work items"

---

## Do's and Don'ts

### Do
- Use the fire orange as the primary accent color
- Maintain high contrast for readability
- Use plenty of whitespace
- Keep the UI clean and focused
- Use subtle animations for feedback
- Use Outfit for all UI headings

### Don't
- Use fire orange for large background areas
- Mix too many colors in one view
- Use light gray text on dark backgrounds (low contrast)
- Overuse animations or transitions
- Add unnecessary decorative elements
- Use Syne for UI headings (reserve for logo only)
