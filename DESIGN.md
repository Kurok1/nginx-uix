---
version: alpha
name: Apple-design-analysis
description: A photography-first interface that turns marketing into a museum gallery. Edge-to-edge product tiles alternate light and dark canvases, framed by SF Pro Display headlines with negative letter-spacing and a single Action Blue (#0066cc) interactive color. UI chrome recedes so the product can speak — no decorative gradients, no shadows on chrome, only the one signature drop-shadow under product imagery resting on a surface.

colors:
  primary: "#0066cc"
  primary-focus: "#0071e3"
  primary-on-dark: "#2997ff"
  ink: "#1d1d1f"
  body: "#1d1d1f"
  body-on-dark: "#ffffff"
  body-muted: "#cccccc"
  ink-muted-80: "#333333"
  ink-muted-48: "#7a7a7a"
  divider-soft: "#f0f0f0"
  hairline: "#e0e0e0"
  canvas: "#ffffff"
  canvas-parchment: "#f5f5f7"
  surface-pearl: "#fafafc"
  surface-tile-1: "#272729"
  surface-tile-2: "#2a2a2c"
  surface-tile-3: "#252527"
  surface-black: "#000000"
  surface-chip-translucent: "#d2d2d7"
  on-primary: "#ffffff"
  on-dark: "#ffffff"
  status-success-foreground: "#1b6e3c"
  status-success-surface: "#eaf7ef"
  status-warning-foreground: "#8a4b00"
  status-warning-surface: "#fff4e5"
  status-error-foreground: "#b42318"
  status-error-surface: "#fef0ef"
  status-unknown-foreground: "#515154"
  status-unknown-surface: "#eeeeef"

typography:
  hero-display:
    fontFamily: "SF Pro Display, system-ui, -apple-system, sans-serif"
    fontSize: 56px
    fontWeight: 600
    lineHeight: 1.07
    letterSpacing: -0.28px
  display-lg:
    fontFamily: "SF Pro Display, system-ui, -apple-system, sans-serif"
    fontSize: 40px
    fontWeight: 600
    lineHeight: 1.1
    letterSpacing: 0
  display-md:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 34px
    fontWeight: 600
    lineHeight: 1.47
    letterSpacing: -0.374px
  lead:
    fontFamily: "SF Pro Display, system-ui, -apple-system, sans-serif"
    fontSize: 28px
    fontWeight: 400
    lineHeight: 1.14
    letterSpacing: 0.196px
  lead-airy:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 24px
    fontWeight: 300
    lineHeight: 1.5
    letterSpacing: 0
  tagline:
    fontFamily: "SF Pro Display, system-ui, -apple-system, sans-serif"
    fontSize: 21px
    fontWeight: 600
    lineHeight: 1.19
    letterSpacing: 0.231px
  body-strong:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 17px
    fontWeight: 600
    lineHeight: 1.24
    letterSpacing: -0.374px
  body:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 17px
    fontWeight: 400
    lineHeight: 1.47
    letterSpacing: -0.374px
  dense-link:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 17px
    fontWeight: 400
    lineHeight: 2.41
    letterSpacing: 0
  caption:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.43
    letterSpacing: -0.224px
  caption-strong:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 14px
    fontWeight: 600
    lineHeight: 1.29
    letterSpacing: -0.224px
  button-large:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 18px
    fontWeight: 300
    lineHeight: 1.0
    letterSpacing: 0
  button-utility:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.29
    letterSpacing: -0.224px
  fine-print:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.0
    letterSpacing: -0.12px
  micro-legal:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 10px
    fontWeight: 400
    lineHeight: 1.3
    letterSpacing: -0.08px
  nav-link:
    fontFamily: "SF Pro Text, system-ui, -apple-system, sans-serif"
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.0
    letterSpacing: -0.12px
  code:
    fontFamily: "ui-monospace, SFMono-Regular, \"SF Mono\", Menlo, Monaco, Consolas, \"Liberation Mono\", \"Courier New\", monospace"
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.54
    letterSpacing: 0

rounded:
  none: 0px
  xs: 5px
  sm: 8px
  md: 11px
  lg: 18px
  pill: 9999px
  full: 9999px

spacing:
  xxs: 4px
  xs: 8px
  sm: 12px
  md: 17px
  lg: 24px
  xl: 32px
  xxl: 48px
  section: 80px

components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
    typography: "{typography.body}"
    rounded: "{rounded.pill}"
    padding: 11px 22px
  button-primary-focus:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
    rounded: "{rounded.pill}"
  button-primary-active:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
    rounded: "{rounded.pill}"
  button-secondary-pill:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.primary}"
    typography: "{typography.body}"
    rounded: "{rounded.pill}"
    padding: 11px 22px
  button-dark-utility:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.on-dark}"
    typography: "{typography.button-utility}"
    rounded: "{rounded.sm}"
    padding: 8px 15px
  button-pearl-capsule:
    backgroundColor: "{colors.surface-pearl}"
    textColor: "{colors.ink-muted-80}"
    typography: "{typography.caption}"
    rounded: "{rounded.md}"
    padding: 8px 14px
  button-store-hero:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
    typography: "{typography.button-large}"
    rounded: "{rounded.pill}"
    padding: 14px 28px
  button-icon-circular:
    backgroundColor: "{colors.surface-chip-translucent}"
    textColor: "{colors.ink}"
    rounded: "{rounded.full}"
    size: 44px
  text-link:
    backgroundColor: transparent
    textColor: "{colors.primary}"
    typography: "{typography.body}"
  text-link-on-dark:
    backgroundColor: transparent
    textColor: "{colors.primary-on-dark}"
    typography: "{typography.body}"
  global-nav:
    backgroundColor: "{colors.surface-black}"
    textColor: "{colors.on-dark}"
    typography: "{typography.nav-link}"
    height: 44px
  sub-nav-frosted:
    backgroundColor: "{colors.canvas-parchment}"
    textColor: "{colors.ink}"
    typography: "{typography.tagline}"
    height: 52px
  product-tile-light:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    typography: "{typography.display-lg}"
    rounded: "{rounded.none}"
    padding: 80px
  product-tile-parchment:
    backgroundColor: "{colors.canvas-parchment}"
    textColor: "{colors.ink}"
    typography: "{typography.display-lg}"
    rounded: "{rounded.none}"
    padding: 80px
  product-tile-dark:
    backgroundColor: "{colors.surface-tile-1}"
    textColor: "{colors.on-dark}"
    typography: "{typography.display-lg}"
    rounded: "{rounded.none}"
    padding: 80px
  product-tile-dark-2:
    backgroundColor: "{colors.surface-tile-2}"
    textColor: "{colors.on-dark}"
    rounded: "{rounded.none}"
  product-tile-dark-3:
    backgroundColor: "{colors.surface-tile-3}"
    textColor: "{colors.on-dark}"
    rounded: "{rounded.none}"
  store-utility-card:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    typography: "{typography.body-strong}"
    rounded: "{rounded.lg}"
    padding: 24px
  configurator-option-chip:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    typography: "{typography.caption}"
    rounded: "{rounded.pill}"
    padding: 12px 16px
  configurator-option-chip-selected:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    rounded: "{rounded.pill}"
  search-input:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.pill}"
    padding: 12px 20px
    height: 44px
  form-field:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    borderColor: "{colors.ink-muted-48}"
    borderWidth: 1px
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    minHeight: 44px
    padding: 9px 12px
  form-field-error:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    borderColor: "{colors.status-error-foreground}"
    borderWidth: 1px
    rounded: "{rounded.sm}"
    minHeight: 44px
  form-field-disabled:
    backgroundColor: "{colors.canvas-parchment}"
    textColor: "{colors.ink-muted-80}"
    borderColor: "{colors.ink-muted-48}"
    borderWidth: 1px
    rounded: "{rounded.sm}"
    minHeight: 44px
  form-field-loading:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    indicatorColor: "{colors.ink-muted-48}"
    borderColor: "{colors.ink-muted-48}"
    borderWidth: 1px
    rounded: "{rounded.sm}"
    minHeight: 44px
  form-help:
    textColor: "{colors.ink-muted-80}"
    typography: "{typography.caption}"
  form-error:
    textColor: "{colors.status-error-foreground}"
    typography: "{typography.caption}"
  status-badge:
    backgroundColor: "{colors.status-unknown-surface}"
    textColor: "{colors.status-unknown-foreground}"
    typography: "{typography.caption-strong}"
    rounded: "{rounded.pill}"
    padding: 4px 10px
  status-badge-success:
    backgroundColor: "{colors.status-success-surface}"
    textColor: "{colors.status-success-foreground}"
    rounded: "{rounded.pill}"
  status-badge-warning:
    backgroundColor: "{colors.status-warning-surface}"
    textColor: "{colors.status-warning-foreground}"
    rounded: "{rounded.pill}"
  status-badge-error:
    backgroundColor: "{colors.status-error-surface}"
    textColor: "{colors.status-error-foreground}"
    rounded: "{rounded.pill}"
  status-badge-unknown:
    backgroundColor: "{colors.status-unknown-surface}"
    textColor: "{colors.status-unknown-foreground}"
    rounded: "{rounded.pill}"
  metric-card:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    borderColor: "{colors.hairline}"
    borderWidth: 1px
    typography: "{typography.body}"
    rounded: "{rounded.lg}"
    padding: 24px
  read-only-code-viewer:
    backgroundColor: "{colors.canvas}"
    textColor: "{colors.ink}"
    mutedTextColor: "{colors.ink-muted-80}"
    borderColor: "{colors.hairline}"
    borderWidth: 1px
    typography: "{typography.code}"
    rounded: "{rounded.lg}"
    padding: 24px
  floating-sticky-bar:
    backgroundColor: "{colors.canvas-parchment}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    height: 64px
    padding: 12px 32px
  environment-quote-card:
    backgroundColor: "{colors.surface-tile-1}"
    textColor: "{colors.on-dark}"
    typography: "{typography.display-lg}"
    rounded: "{rounded.none}"
    padding: 80px
  footer:
    backgroundColor: "{colors.canvas-parchment}"
    textColor: "{colors.ink-muted-80}"
    typography: "{typography.fine-print}"
    padding: 64px
---

## Overview

Apple's web presence is a masterclass in **reverent product photography framed by near-invisible UI**. Every page is a stack of edge-to-edge product "tiles" — alternating light and dark canvases, each centered on a hero headline, a one-line tagline, two tiny blue pill CTAs, and an impossibly crisp product render. Nothing competes with the product. Typography is confident but quiet; color is either pure white, an off-white parchment, or a near-black tile; interactive elements are a single, quiet blue.

Density is unusually low even by contemporary SaaS standards. Each tile occupies roughly one viewport, and there is no decorative chrome — no borders, no gradients, no decorative frames, no shadows on headlines. Elevation appears only when a product image rests on a surface (a single soft `rgba(0, 0, 0, 0.22) 3px 5px 30px` drop for visual weight). The result is a catalog that feels more like a museum gallery: the wall disappears and the artifact takes over.

Store and shop surfaces retain the same chassis but switch modes. The product configurator (iPhone 17 Pro, accessories grid) introduces a tight grid of white utility cards at `{rounded.lg}` (18px) radius with a thin border, paired with a persistent thin sub-nav strip. The environment page leans darker and more editorial. Across all five surfaces the typographic system, spacing rhythm, and the single blue accent are consistent — this is one design language expressed at different volumes.

**Key Characteristics:**
- Photography-first presentation; UI recedes so the product can speak.
- Alternating full-bleed tile sections: white/parchment ↔ near-black, with the color change itself acting as the section divider.
- Single blue accent (`{colors.primary}` — #0066cc) carries every interactive element. No second brand color exists.
- Two button grammars: tiny blue pill CTAs (`{rounded.pill}`) and compact utility rects (`{rounded.sm}`).
- SF Pro Display + SF Pro Text — negative letter-spacing at display sizes for the signature "Apple tight" headline feel.
- Whisper-soft elevation used only when a product image needs to breathe — exactly one drop-shadow in the entire system.
- Tight two-row nav: slim `{component.global-nav}` + product-specific `{component.sub-nav-frosted}` with persistent right-aligned primary CTA.
- Section rhythm across multiple pages: light hero → dark product tile → light utility tile → dark tile → parchment footer — a predictable pulse.

Operational surfaces use a denser expression of the same language rather than a separate admin theme. Forms, runtime metrics, status badges, and effective-configuration views keep the system type, hairlines, radii, spacing, and flat chrome. Semantic status colors communicate health only; they do not alter the one-accent interaction grammar.

## Colors

> **Source pages analyzed:** homepage, environment, store, iPhone 17 Pro buy page, accessories index. The color system is identical across all five surfaces; only the surface-mode mix differs.

### Brand & Accent
- **Action Blue** (`{colors.primary}` — #0066cc): The single brand-level interactive color. All text links, all blue pill CTAs ("Learn more", "Buy"), and the focus ring root. This is Apple's quiet but universal "click me" signal. Press state shifts to a slightly darker variant via the active scale transform rather than a hex change.
- **Focus Blue** (`{colors.primary-focus}` — #0071e3): A marginally brighter sibling of Action Blue, reserved for the keyboard focus ring on buttons (`outline: 2px solid`).
- **Sky Link Blue** (`{colors.primary-on-dark}` — #2997ff): A brighter blue used on dark surfaces for in-copy links and inline callouts, where Action Blue would disappear against the tile background.

### Surface
- **Pure White** (`{colors.canvas}` — #ffffff): The dominant canvas. Content, utility cards, store tiles, configurator grids.
- **Parchment** (`{colors.canvas-parchment}` — #f5f5f7): The signature Apple off-white. Used for alternating light tiles, footer region, and the default page canvas in store utility sections. Just different enough from white to create rhythm.
- **Pearl Button** (`{colors.surface-pearl}` — #fafafc): A near-white used as the fill for secondary "ghost" buttons — lighter than the parchment canvas so the button still reads as a button against `{colors.canvas-parchment}`.
- **Near-Black Tile 1** (`{colors.surface-tile-1}` — #272729): The primary dark-tile surface on the homepage product grid.
- **Near-Black Tile 2** (`{colors.surface-tile-2}` — #2a2a2c): A micro-step lighter — used where a dark tile sits directly above or below Tile 1 to create the faintest separation.
- **Near-Black Tile 3** (`{colors.surface-tile-3}` — #252527): A micro-step darker — used at the bottom of the stack and in embedded video/player frames.
- **Pure Black** (`{colors.surface-black}` — #000000): Reserved for true void — video player backgrounds, edge-to-edge photographic overlays, the global nav bar background.
- **Translucent Chip Gray** (`{colors.surface-chip-translucent}` — #d2d2d7): The base hex of the translucent gray chip used over photography for circular control buttons. In production, applied at ~64% alpha as `rgba(210, 210, 215, 0.64)`.

### Text
- **Near-Black Ink** (`{colors.ink}` — #1d1d1f): The voice of every headline, every body paragraph, and the dark utility button's fill. Chosen instead of pure black to keep the page feeling photographic rather than printed.
- **Body** (`{colors.body}` — #1d1d1f): Same hex as ink — Apple uses one near-black tone for all text on light surfaces.
- **Body On Dark** (`{colors.body-on-dark}` — #ffffff): All text on dark tiles and on the global nav bar.
- **Body Muted** (`{colors.body-muted}` — #cccccc): Secondary copy on dark tiles where pure white would be too loud.
- **Ink Muted 80** (`{colors.ink-muted-80}` — #333333): Body text on the white Pearl Button surface — slightly softer than pure black.
- **Ink Muted 48** (`{colors.ink-muted-48}` — #7a7a7a): Disabled button text and legal fine-print.

### Hairlines & Borders
- **Divider Soft** (`{colors.divider-soft}` — #f0f0f0): The "border" tone on secondary buttons — functions as a ring shadow rather than a hard line. In production, often applied as `rgba(0, 0, 0, 0.04)`.
- **Hairline** (`{colors.hairline}` — #e0e0e0): The 1px hairline border on store utility cards and configurator chips.

### Operational Status (Status Only)

The following tokens are four inseparable foreground/surface pairs. They are reserved exclusively for semantic state and health: never use them for links, buttons, selected navigation, keyboard focus, chart categories, branding, or decoration. **Action Blue (`{colors.primary}`) remains the sole interaction accent.**

- **Success:** `{colors.status-success-foreground}` (#1b6e3c) on `{colors.status-success-surface}` (#eaf7ef).
- **Warning:** `{colors.status-warning-foreground}` (#8a4b00) on `{colors.status-warning-surface}` (#fff4e5).
- **Error:** `{colors.status-error-foreground}` (#b42318) on `{colors.status-error-surface}` (#fef0ef).
- **Unknown:** `{colors.status-unknown-foreground}` (#515154) on `{colors.status-unknown-surface}` (#eeeeef).

Every status presentation combines its color pair with visible text and a distinct icon or shape. Success uses a check in a circle, warning an exclamation in a triangle, error an exclamation in an octagon, and unknown a question mark in a diamond. The icon is redundant to the adjacent status text and is hidden from assistive technology; the text supplies the accessible name.

### Brand Gradient
**No decorative gradients.** Atmospheric depth on product photography (the iPhone 17 Pro camera plate, the Apple Watch bands, AirPods reflections) is inherent to the imagery, not a CSS gradient overlay. The environment page's hero uses photographic atmosphere (mountain vista at dawn) but no gradient tokens are defined. Apple is the rare luxury-brand site with zero gradient-based design tokens.

## Typography

### Font Family
- **Display**: `SF Pro Display, system-ui, -apple-system, sans-serif` — Apple's proprietary display face, optimized for sizes ≥ 19px. Defines the voice of every headline.
- **Body / UI**: `SF Pro Text, system-ui, -apple-system, sans-serif` — the text-optimized variant used for body copy, captions, buttons, and links below 20px.
- **Code / Configuration**: `ui-monospace, SFMono-Regular, "SF Mono", Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace` — the system monospace stack for effective Nginx configuration and other literal machine output. UI labels and controls around code remain in the Body / UI stack.
- **OpenType features**: `font-variant-numeric: numerator` is enabled on numeric links (pricing tables, spec sheets). Display sizes rely on tight tracking rather than contextual ligatures.

### Hierarchy

| Token | Size | Weight | Line Height | Letter Spacing | Use |
|---|---|---|---|---|---|
| `{typography.hero-display}` | 56px | 600 | 1.07 | -0.28px | Hero headline; the signature "Apple tight" tracking |
| `{typography.display-lg}` | 40px | 600 | 1.10 | 0 | Tile headlines atop every product tile |
| `{typography.display-md}` | 34px | 600 | 1.47 | -0.374px | Section heads (SF Pro Text at display proportions) |
| `{typography.lead}` | 28px | 400 | 1.14 | 0.196px | Product tile subcopy |
| `{typography.lead-airy}` | 24px | 300 | 1.5 | 0 | Environment-page lead paragraphs (the rare weight 300) |
| `{typography.tagline}` | 21px | 600 | 1.19 | 0.231px | Sub-tile tagline; sub-nav category name |
| `{typography.body-strong}` | 17px | 600 | 1.24 | -0.374px | Inline strong emphasis |
| `{typography.body}` | 17px | 400 | 1.47 | -0.374px | Default paragraph |
| `{typography.dense-link}` | 17px | 400 | 2.41 | 0 | Footer / store utility link lists (relaxed leading) |
| `{typography.caption}` | 14px | 400 | 1.43 | -0.224px | Secondary captions, button text |
| `{typography.caption-strong}` | 14px | 600 | 1.29 | -0.224px | Emphasized captions |
| `{typography.button-large}` | 18px | 300 | 1.0 | 0 | Store hero CTAs (the rare weight 300) |
| `{typography.button-utility}` | 14px | 400 | 1.29 | -0.224px | Utility/nav button labels |
| `{typography.fine-print}` | 12px | 400 | 1.0 | -0.12px | Fine-print, footer body |
| `{typography.micro-legal}` | 10px | 400 | 1.3 | -0.08px | Micro legal disclaimers |
| `{typography.nav-link}` | 12px | 400 | 1.0 | -0.12px | Global nav menu items |
| `{typography.code}` | 13px | 400 | 1.54 | 0 | Read-only configuration and literal machine output |

### Principles

- **Negative letter-spacing at display sizes.** Every headline at 17px and up carries a slight tracking tighten (`-0.12 → -0.374px`). This produces the iconic "Apple tight" headline cadence. Never used at 12px or below.
- **Body copy at 17px, not 16px.** Apple breaks the SaaS convention and runs paragraph text at 17px. The extra pixel gives the page an unmistakable "reading, not scanning" pace.
- **Weight 300 is real and rare.** Used deliberately on a handful of large-size reads (`{typography.button-large}` at 18px/300 and `{typography.lead-airy}` at 24px/300). It's not an accident — it's a light-atmosphere cue reserved for moments where the content should feel airy.
- **Weight 600, not 700, for headlines.** Apple's headlines sit at weight 600. Weight 700 is used sparingly for `{typography.tagline}` (21px) when a touch more assertion is needed.
- **Line-height is context-specific.** Display sizes use 1.07–1.19 (tight). Body uses 1.47. Utility link stacks in the footer/store use an unusually relaxed 2.41 (`{typography.dense-link}`). The 2.41 is not a bug — it's how the footer's dense link columns breathe.
- **Weight 500 is deliberately absent.** The ladder is 300 / 400 / 600 / 700. Mid-weight readings always use 600.
- **Monospace is content, not chrome.** Only literal configuration and machine output use `{typography.code}`. File selectors, line-wrap controls, labels, badges, and helper text stay on the system sans-serif UI stack.

### Note on Font Substitutes
SF Pro is Apple's proprietary system font. When building off-system:

- Use `system-ui, -apple-system, BlinkMacSystemFont` as the first stack entry — on macOS/iOS/Safari this resolves to the real SF Pro.
- For non-Apple platforms, **Inter** (Google Fonts, variable) is the closest open-source equivalent. Inter at weight 600 with `font-feature-settings: "ss03"` approximates SF Pro's rounded "a" character.
- Nudge `letter-spacing` down by `-0.01em` on display sizes to re-create the Apple tight feel; Inter's default tracking runs slightly wider than SF Pro.
- For body text, tighten line-height by `0.03` (from 1.47 → 1.44) when substituting Inter — Inter's taller x-height needs less leading.

## Layout

### Spacing System
- **Base unit:** 8px. Sub-base values (2, 4, 5, 6, 7) are used for tight typographic adjustments; structural layout snaps to 8/12/16/20/24.
- **Tokens:** `{spacing.xxs}` 4px · `{spacing.xs}` 8px · `{spacing.sm}` 12px · `{spacing.md}` 17px · `{spacing.lg}` 24px · `{spacing.xl}` 32px · `{spacing.xxl}` 48px · `{spacing.section}` 80px.
- **Section vertical padding:** `{spacing.section}` (80px) inside a product tile; tiles stack edge-to-edge with 0 gap (the color change provides the break).
- **Card padding:** `{spacing.lg}` (24px) inside utility grid cards.
- **Button padding:** 8–11px vertical, 15–22px horizontal.
- **Universal rhythm constants:** the 17px body line-height multiplier (~25px line) and 21px tagline size show up on every analyzed page.

### Grid & Container
- **Max content width:** ~980px on text-heavy sections (environment), ~1440px on product grids (store, accessories), full-bleed for product tiles (homepage).
- **Column patterns:** 3 to 5 column utility card grid on store/accessories; 2-column side-by-side tiles on homepage occasional sections; single-column centered stack on product tile heroes.
- **Gutters:** 20–24px between cards in a utility grid.

### Operational Layout

- Dashboard metrics use a three-column baseline grid with `{spacing.lg}` (24px) gutters. Each child has `min-width: 0` so long runtime values wrap inside the card rather than widening the page.
- The effective-configuration surface is two-column above 734px: a bounded file/order navigator beside one `{component.read-only-code-viewer}`. The navigator never competes with the code area for horizontal page scroll.
- Operational cards and code containers use `{rounded.lg}` (18px), `{spacing.lg}` (24px) internal padding, and a 1px `{colors.hairline}` border. They never use the product shadow or a decorative gradient.

### Whitespace Philosophy
Apple's whitespace is the product's pedestal. Every tile begins with at least 64px of air above its headline and 48–64px below. Product renders are never crowded; the nearest content to a product image is at least 40px away. The footer is the only area that breaks this — there, Apple goes deliberately dense to make the full information architecture visible at a glance.

## Elevation & Depth

| Level | Treatment | Use |
|---|---|---|
| Flat | No shadow, no border | Full-bleed tiles, global nav, footer, body sections |
| Soft hairline | 1px `rgba(0, 0, 0, 0.08)` border | Utility cards, sub-nav frosted-glass separator |
| Backdrop blur | `backdrop-filter: blur(N)` on Parchment 80% | Sub-nav and the iPhone buy floating sticky bar |
| Product shadow | `rgba(0, 0, 0, 0.22) 3px 5px 30px 0` | Product renders resting on a surface (the only true "shadow" in the system) |

**Shadow philosophy.** Apple uses **exactly one** drop-shadow, and it is applied to photographic product imagery — never to cards, never to buttons, never to text. Elevation in the UI comes from (a) surface-color change (light tile ↔ dark tile) and (b) backdrop-blur on sticky bars. The single shadow is about giving the product weight, not about UI hierarchy.

### Decorative Depth
- **Atmospheric imagery** on the environment page (photographic vista) supplies mood; no CSS gradient involved.
- **Edge-to-edge tile alternation** creates rhythm without borders or shadows — the color change itself is the divider.
- **Backdrop-filter blur** on `{component.sub-nav-frosted}` and `{component.floating-sticky-bar}` creates a "floating over content" effect that's functional, not decorative.

## Shapes

### Border Radius Scale

| Token | Value | Use |
|---|---|---|
| `{rounded.none}` | 0px | Full-bleed product tiles (no corner rounding) |
| `{rounded.xs}` | 5px | Inline links when styled as subtle chips (rare) |
| `{rounded.sm}` | 8px | Dark utility buttons (Sign In, Bag), inline card imagery |
| `{rounded.md}` | 11px | White Pearl Button capsules |
| `{rounded.lg}` | 18px | Store utility cards, accessories grid cards |
| `{rounded.pill}` | 9999px | Primary blue pill CTAs, sub-nav buy button, configurator option chips, search input — the signature Apple pill |
| `{rounded.full}` | 9999px / 50% | Circular control chips floating over photography |

### Photography Geometry
- **Hero imagery**: full-bleed, 21:9 or taller on the homepage; 16:9 on environment and shop pages. Product renders are photographic-realistic, often shot on a tinted surface that becomes the tile background.
- **Product renders**: PNG/WebP with transparency; rest on a surface tile and pick up the system shadow.
- **Accessory grid**: square 1:1 crops at `{rounded.lg}` (18px) radius, light neutral backgrounds, product centered with 20–40px internal padding.
- **No rounded imagery in hero tiles** — images are full-bleed rectangular. Rounding (`{rounded.sm}`, `{rounded.lg}`) appears only on inline card imagery.
- Lazy-loading via responsive `srcset` and `sizes` across all breakpoints; CDN-optimized WebP.

## Components

### Top Navigation

**`global-nav`** — Persistent, ultra-thin black nav bar pinned to the top of every page. Background `{colors.surface-black}`, height 44px, text `{colors.on-dark}` in `{typography.nav-link}` (12px / 400 / -0.12px tracking). Links are quiet, spaced ~20px apart, running edge-to-edge across the top. Right-aligned cluster: Search, Bag icons — always visible. On mobile, collapses to hamburger at ~834px and the Apple logo centers.

**`sub-nav-frosted`** — Surface-specific nav that sticks below the global nav. Background `{colors.canvas-parchment}` at 80% opacity with backdrop-filter blur, creating a frosted-glass effect. Height 52px. Content on left: product category name ("iPhone", "Store", "Accessories") in `{typography.tagline}` (21px / 600). Content right: inline nav links in `{typography.button-utility}` (14px), ending in a persistent `{component.button-primary}` ("Buy") or a utility link.

### Buttons

**`button-primary`** — The signature Apple action. Background `{colors.primary}` (Action Blue #0066cc), text `{colors.on-primary}` in `{typography.body}` (SF Pro Text 17px / 400), rounded `{rounded.pill}` (full pill — capsule-shaped), padding 11px × 22px. The full-pill radius IS the brand action signal.
- Active state: `{component.button-primary-active}` — `transform: scale(0.95)` (the system-wide micro-interaction).
- Focus state: `{component.button-primary-focus}` — 2px solid `{colors.primary-focus}` outline.

**`button-secondary-pill`** — Used as the second CTA when two blue pills appear together ("Learn more" / "Buy"). Background transparent, text `{colors.primary}`, 1px solid `{colors.primary}` border, rounded `{rounded.pill}`, padding 11px × 22px. Reads as a "ghost pill."

**`button-dark-utility`** — Global nav actions (Sign In, Bag, language selector). Background `{colors.ink}` (#1d1d1f), text `{colors.on-dark}` in `{typography.button-utility}` (14px / 400 / -0.224px tracking), rounded `{rounded.sm}` (8px), padding 8px × 15px. Active state shrinks via `transform: scale(0.95)`.

**`button-pearl-capsule`** — Product-card secondary button. Background `{colors.surface-pearl}` (#fafafc), text `{colors.ink-muted-80}` in `{typography.caption}` (14px), 3px solid `{colors.divider-soft}` border (functions as a soft ring rather than a visible line), rounded `{rounded.md}` (11px), padding 8px × 14px.

**`button-store-hero`** — A larger primary CTA used on store hero surfaces. Same Action Blue + Paper White as `{component.button-primary}`, but with `{typography.button-large}` (18px / 300 — note the rare weight 300) and slightly more padding (14px × 28px). Used sparingly on the store landing.

**`button-icon-circular`** — Floats over photography. 44 × 44px, background `{colors.surface-chip-translucent}` at ~64% alpha, icon in `{colors.ink}`, rounded `{rounded.full}`. Used for carousel controls, close buttons, and in-image controls (product image thumbnails on the iPhone buy page).

**`text-link`** — Inline body links in `{colors.primary}` (Action Blue). Underlined or non-underlined per context.

**`text-link-on-dark`** — Inline body links on dark tiles in `{colors.primary-on-dark}` (Sky Link Blue #2997ff) — Action Blue would disappear against `{colors.surface-tile-1}`.

### Cards & Containers

**`product-tile-light`** — Full-bleed light tile. Background `{colors.canvas}` (white), text `{colors.ink}`, rounded `{rounded.none}` (0 — tiles touch edges), vertical padding `{spacing.section}` (80px). Centered stack: product name in `{typography.display-lg}` (40px / 600) → one-line tagline in `{typography.lead}` (28px / 400) → two `{component.button-primary}` CTAs ("Learn more" / "Buy") → product render resting on the surface with the system shadow.

**`product-tile-parchment`** — Same as `{component.product-tile-light}` but on `{colors.canvas-parchment}` (#f5f5f7). Used to break two consecutive white tiles.

**`product-tile-dark`** — Full-bleed dark tile. Background `{colors.surface-tile-1}` (#272729), text `{colors.on-dark}`, rounded `{rounded.none}`, vertical padding `{spacing.section}` (80px). Same content stack as the light tile but with `{component.text-link-on-dark}` for inline copy and `{component.button-primary}` (Action Blue still works on the dark surface). Used on the homepage product grid as the alternating dark band.

**`product-tile-dark-2`** — Variant on `{colors.surface-tile-2}` (#2a2a2c). Used where a dark tile sits directly above or below `{component.product-tile-dark}` to create the faintest separation through micro-step lightness change.

**`product-tile-dark-3`** — Variant on `{colors.surface-tile-3}` (#252527). Used at the bottom of the stack and in embedded video/player frames.

**`store-utility-card`** — Used in store grid and accessories grid. Background `{colors.canvas}` (white), 1px solid `{colors.hairline}` border, rounded `{rounded.lg}` (18px), padding `{spacing.lg}` (24px). Top: product image (1:1 crop with `{rounded.sm}` (8px) inner image radius). Below: product name in `{typography.body-strong}` (17px / 600), price in `{typography.body}` (17px / 400), and a `{component.text-link}` ("Buy" or "Learn more"). No shadow by default; product render itself carries the system product-shadow.

**`metric-card`** — The dashboard's compact readout container. Background `{colors.canvas}`, 1px `{colors.hairline}` border, rounded `{rounded.lg}` (18px), padding `{spacing.lg}` (24px), and no chrome shadow or gradient. A short heading identifies the metric, the primary value remains `{colors.ink}`, and optional supporting copy uses `{typography.caption}`. Represent label/value data as a semantic `<dl>` or a labelled `<article>`; do not make the whole card look clickable. An optional `{component.status-badge}` carries health, while any real action is a separate labelled control with a minimum 44 × 44px target.

During refresh, set `aria-busy="true"` on only the affected card or metric group and preserve its dimensions. Announce a concise result in the nearest `aria-live="polite"` region only after a user-requested refresh or a meaningful state change; do not make the entire grid live or repeatedly announce timers. Loading, error, and unknown values always show a short text phrase plus their progress/status icon.

**`configurator-option-chip`** — Pill-shaped tappable cell used in the iPhone 17 Pro buy page. Background `{colors.canvas}`, text `{colors.ink}` in `{typography.caption}`, rounded `{rounded.pill}`, padding 12px × 16px. Contains a small product thumbnail + label + price delta. Arranged in a grid of 4–5 options per row.

**`configurator-option-chip-selected`** — Selected state. Border upgrades to 2px solid `{colors.primary-focus}`. Same shape, same content.

**`environment-quote-card`** — A photographic-canvas hero specific to the environment page. Dark photographic backdrop (mountain vista at dawn) with `{colors.surface-tile-1}` as the fallback color, centered white-text headline in `{typography.display-lg}` (40px), small green "Apple 2030" pictographic logo above the headline, single `{component.button-primary}` below. Padding `{spacing.section}` (80px).

**`floating-sticky-bar`** — Floats at the bottom of the viewport on the iPhone 17 Pro buy page during scroll. Background `{colors.canvas-parchment}` at 80% opacity with `backdrop-filter: blur(N)`, height 64px, padding 12px × 32px. Left: running price total in `{typography.body}`. Right: `{component.button-primary}` ("Add to Bag").

### Inputs & Forms

**`search-input`** — The accessories search input. Background `{colors.canvas}`, text `{colors.ink}` in `{typography.body}` (17px), 1px solid `rgba(0, 0, 0, 0.08)` border, rounded `{rounded.pill}` (full pill — search is also pill-shaped, matching the CTA grammar), padding 12px × 20px, height 44px. Leading icon: search glyph at 14px, muted tint.

**`form-field`** — The neutral operational input, select, or textarea. Background `{colors.canvas}`, text `{colors.ink}` in `{typography.body}`, 1px `{colors.ink-muted-48}` border, rounded `{rounded.sm}` (8px), horizontal padding 12px, and minimum height 44px. The stronger neutral border gives the control boundary at least 3:1 contrast; `{colors.hairline}` remains the non-essential grouping border for cards and code containers. Every field has a persistent visible `<label>`; placeholders are examples, never labels. Focus uses a 2px `{colors.primary-focus}` outline with offset, so Action Blue remains the only interactive focus signal.

**`form-help`** — Optional supporting text immediately after a field, in `{typography.caption}` and `{colors.ink-muted-80}`. Give it a stable ID and include that ID in the field's `aria-describedby` token list.

**`form-error`** — A specific error message in `{typography.caption}` and `{colors.status-error-foreground}`, preceded by the error octagon icon. `{component.form-field-error}` changes the resting border to `{colors.status-error-foreground}`, but focus still uses Action Blue and the icon/text remain visible; color is never the only error cue. Set `aria-invalid="true"` and append the error message ID to `aria-describedby` without dropping the help ID. New submit-time errors may be announced once through the nearest restrained `aria-live="polite"` region; reserve `role="alert"` for a blocking safety failure, never every keystroke.

**Disabled behavior** — `{component.form-field-disabled}` uses `{colors.canvas-parchment}`, `{colors.ink-muted-80}`, and the neutral `{colors.ink-muted-48}` boundary while keeping its content legible. Use native `disabled` for native controls; custom controls require `aria-disabled="true"` plus actual event suppression. Show a nearby reason when the cause is not obvious. Do not communicate disabled state through reduced opacity or color alone, and do not leave a disabled-looking control operable.

**Loading behavior** — `{component.form-field-loading}` preserves the control's size and current value, adds a progress indicator plus a visible verb phrase such as “Loading…” or “Saving…”, and sets `aria-busy="true"` on the smallest affected group. A submitting button may use native `disabled` to prevent repeats, but its visible label must change to the in-progress phrase and the result is announced once by a nearby `aria-live="polite"` status. Under `prefers-reduced-motion: reduce`, use a static progress glyph with the same text instead of continuous spin.

### Status & Feedback

**`status-badge`** — A compact, non-interactive status label in `{typography.caption-strong}`, rounded `{rounded.pill}`, with 4px × 10px padding. The base and `{component.status-badge-unknown}` use the Unknown pair; `{component.status-badge-success}`, `{component.status-badge-warning}`, and `{component.status-badge-error}` use their matching foreground/surface pairs. Every badge includes the visible state word plus its prescribed shape/icon. The redundant icon is `aria-hidden="true"`; never publish an unlabeled colored dot. If a status needs an action, place a separate 44 × 44px minimum button or link beside it rather than making the badge interactive.

Static badges need no live-region role. For asynchronous status changes, put one concise text node in the closest owner region with `aria-live="polite"` and `aria-atomic="true"`; do not attach `aria-live` to the page shell, metric grid, or rapidly updating log. A blocking error may use a one-time alert at the task boundary, but subsequent detail remains ordinary readable content.

### Read-only Configuration

**`read-only-code-viewer`** — A labelled, read-only region for effective Nginx configuration and bounded machine output. Background `{colors.canvas}`, 1px `{colors.hairline}` border, rounded `{rounded.lg}` (18px), padding `{spacing.lg}` (24px), no chrome shadow, and no gradient. The header, filename, source-order metadata, and controls use the system sans-serif stack; selectable `<pre><code>` content uses `{typography.code}`. Do not use `contenteditable`, an editor role, or canvas-rendered text for this read-only surface.

- Associate the region with a visible heading through `aria-labelledby`; give the scroll container an additional concise label describing the selected file.
- Keep configuration text selectable. Render visual line numbers in a separate `aria-hidden="true"` gutter so they are not announced or copied with the configuration.
- Provide a labelled “Wrap lines” button with `aria-pressed`, a minimum 44 × 44px target, and a visible Action Blue focus outline. Wrapping is a viewing preference, not a content mutation.
- Make the scroll container keyboard-focusable and preserve native Arrow, Page Up/Down, Home/End, and horizontal scrolling behavior. Its focus ring must remain visible when content is scrolled.
- With wrapping off, long code lines scroll horizontally inside the viewer. The viewer may also scroll vertically within a bounded height; neither axis may create horizontal page overflow.

### Configuration Workspace

The configuration workspace extends the read-only configuration surface. v0.2.1 editing remains confined to the draft; v0.2.2 adds an explicit checked publication flow that can back up and update production and reload Nginx. Publication is never implicit in Save, diff, navigation, modal close, or browser disconnect. Restart, arbitrary restore, and arbitrary commands remain unavailable. Use the existing system font, spacing, radii, neutral borders, and Action Blue focus treatment; do not introduce a gradient, decorative shadow, second accent color, or a business-component hex value.

#### Operational Tokens

The following literal token contract is stable for CSS and tests:

```text
--color-state-success / warning / danger / info: semantic status only, never brand emphasis
--color-diff-added / removed / context: status surfaces with non-color +/−/line labels
--component-workspace-tree-width: 240px
--component-workspace-tree-width-narrow: 208px
--component-workspace-review-width: 360px
--component-workspace-header-min-height: 56px
--component-editor-min-height: 480px
--component-drawer-width: min(92vw, 520px)
--component-modal-width: min(calc(100vw - 32px), 480px)
--component-release-timeline-marker: 24px
--component-release-diagnostic-max-height: 240px
```

`--color-state-success`, `--color-state-warning`, `--color-state-danger`, and `--color-state-info` are semantic status tokens only, never brand emphasis. `--color-diff-added`, `--color-diff-removed`, and `--color-diff-context` are status surfaces only; every added, removed, and context line also has its visible `+`, `−`, or context-line label. These tokens inherit the existing semantic foreground/surface approach and must never make a colored surface the sole state signal.

#### Workspace Layout and Components

At desktop width, the workspace is a continuous three-pane review: tree, editor, and review. The tree uses `--component-workspace-tree-width`; the review pane uses `--component-workspace-review-width`; the editor is the flexible middle pane with `min-width: 0` and at least `--component-editor-min-height`. The workspace header is at least `--component-workspace-header-min-height`. Preserve all pane-level scrolling inside the pane rather than creating horizontal page overflow.

**`workspace-tree`** — tree: ARIA tree/treeitem, arrows, Home/End, text+icon state, 44px target. Use the semantic `tree` and `treeitem` roles with the expected parent/child levels and expanded state. Arrow keys move, expand, and collapse according to the ARIA tree pattern; Home and End move to the first and last visible item. Each physical file, logical group, external/missing entry, and read-only reason combines visible text with its icon, and every interactive row or disclosure has a 44 × 44px minimum target and visible keyboard focus.

**`workspace-editor`** — editor: explicit Save, dirty text marker, internal horizontal scroll, no autosave/persistence. The header has a visible file name, a text dirty marker such as “Unsaved changes”, and an explicit “Save” action. A Save action is disabled with its reason when there is no change, a request is in progress, or the workspace is read-only. Browser memory may retain the current unsubmitted text during the active session only: do not autosave or persist it to `localStorage`, IndexedDB, Cache Storage, Service Worker cache, or the URL. Long code lines scroll horizontally inside the editor; they never cause page-level horizontal scrolling.

**`workspace-diff`** — diff: unified lines, line numbers, +/- labels, per-file summary, response-limit incomplete state. Show a per-file summary before its unified lines, preserve line numbers, and give each added or removed line a visible `+` or `−` label in addition to its semantic surface. If a diff response reaches `response_limit`, render the persistent incomplete state “Diff incomplete: response limit reached”; do not imply that omitted lines are unchanged or that the review is complete.

**`workspace-drawer`** and **`workspace-modal`** — drawer/modal: focus trap, Escape, background inert, trigger focus restoration. A drawer contains the review pane at `--component-drawer-width`; a modal uses `--component-modal-width`. Both have a visible accessible name, trap focus while open, close on Escape, make the background inert, and restore focus to their invoking trigger after close. The drawer is the review surface at intermediate widths; the modal is reserved for named confirmation and never hides critical conflict, stale, or needs-attention information.

**`workspace-toast`** — toast: non-critical success only; conflict/stale/needs_attention stay inline. Toasts may announce a completed non-critical action once through the nearest restrained polite live region. Conflict, stale, `needs_attention`, capacity failures, and Agent-unavailable states remain persistently visible inline with their context, action, and non-color status cue; they are never reduced to a transient toast.

**`publish-check-panel`** — A persistent neutral review card beneath the workspace diff. Before a check it shows the exact blocking reason when unavailable: dirty browser documents, incomplete or empty diff, non-`ready` state, another mutation/task, or Agent failure. During checking it preserves its dimensions, uses `aria-busy="true"`, and names the action “Checking complete candidate…”. A valid result displays production/draft/candidate identity abbreviations, validator build, check and expiry times, and the explicit sentence “Production configuration has not been changed.” Invalid diagnostics use the read-only code-viewer language: selectable text, bounded internal scroll at `--component-release-diagnostic-max-height`, relative path and line only, no editor role, absolute path, raw stderr, or secrets.

**`release-confirmation-modal`** — A named confirmation modal at `--component-modal-width`. It states that the system will recheck production and the draft, create a complete backup, update production files, run full validation, reload Nginx, and automatically roll back when the result is safely knowable. The confirmation input must exactly equal the visible workspace name before the primary action is enabled. It follows `workspace-modal` focus trap, Escape, inert background, 44px target, and trigger-focus restoration rules. Closing it before submission has no effect; after a task is queued, closing it or leaving the page does not cancel the task.

**`release-stage-timeline`** — An ordered list of persisted release stages. Each row contains a `--component-release-timeline-marker` icon/shape, visible stage name, visible status word, and timestamp; the connecting hairline is neutral and never a progress gradient. Only the current concise stage phrase uses a local `aria-live="polite"`/`aria-atomic="true"`; historical rows and SSE heartbeats are not live. Refresh and reconnect rebuild the list from the release resource and `Last-Event-ID`, never from elapsed browser time. Terminal panels remain inline and distinguish: published and healthy; failed before production changed; failed but rolled back and healthy; or `needs_attention`. The last case is a blocking alert with evidence-only actions and no v0.2.3 restore control.

#### Operational State Examples

All workspace state includes visible text plus its status icon/shape; no state relies on color alone. Keep each state local to the affected panel and use the smallest appropriate `aria-live` region.

| State | Required visible example and behavior |
| --- | --- |
| Loading | “Loading workspace files…” with a progress glyph; keep the affected pane’s dimensions, set `aria-busy="true"` on that pane, and preserve already loaded content. |
| Empty | “No managed configuration files are available in this workspace.” Explain the empty result and offer only the applicable next action. |
| Error | “Could not save this file. Your local changes are still available.” Keep the editor text and provide a retry or copy action without exposing internal details. |
| Conflict | “This file changed on the server. Your local text has not been overwritten.” Keep a persistent inline banner with “Copy local content”, “Read server version”, and “View server diff”; do not retry or overwrite automatically. |
| Stale | “Production configuration changed. Create a new workspace to continue.” The old workspace is read-only and the message remains inline. |
| needs-attention (`needs_attention`) | “Workspace consistency cannot be confirmed.” Show the workspace ID, make ordinary saving unavailable, and permit only viewing or named deletion. |
| Published (`published`) | “This immutable workspace was published successfully.” Show its release ID, keep files and diff readable, and make editing and repeat publication unavailable. |
| Release rolled back | “Publication failed. The previous configuration was restored and runtime health was confirmed.” Keep the release and backup IDs plus stage evidence visible. |
| Release needs attention | “Production or runtime state cannot be confirmed.” Use a blocking inline alert; permit evidence review only and do not offer retry, restart, or restore in v0.2.2. |
| Agent-unavailable | “Configuration Agent is unavailable. Production configuration and files are unaffected.” Keep the workspace inline state visible and do not fall back to direct production-file access. |
| Diff incomplete | “Diff incomplete: response limit reached.” Retain the per-file `response_limit` state and do not present the available portion as a complete review. |

Named confirmation modals state their actual scope and whether production configuration or files are unaffected:

- **Delete file “`<filename>`”?** “This deletes `'<filename>'` only from this workspace draft. Production configuration and files are unaffected.”
- **Delete workspace “`<workspace name>`”?** “This removes the workspace draft and its metadata. Production configuration and files are unaffected.”
- **Delete logical group “`<group name>`”?** “This removes only the logical group. It does not delete files, and production configuration is unaffected.”
- **Publish workspace “`<workspace name>`” and reload Nginx?** “The system will recheck production and the draft, create a complete backup, update production configuration, validate it, and reload Nginx. Safely knowable failures are rolled back; uncertain outcomes require manual attention.”

#### Configuration Workspace Responsive Behavior

breakpoints: 1069 / 1068 / 834 / 833 / 735 / 734 / 640 CSS px

| Width | Required workspace layout |
| --- | --- |
| `>= 1069px` | Show tree, editor, and review together. Use `--component-workspace-tree-width` for the tree and `--component-workspace-review-width` for review. |
| `834–1068px` | Show tree and editor. Move review into `workspace-drawer`. |
| `735–833px` | Show the narrowed tree at `--component-workspace-tree-width-narrow` with the editor. Keep review in `workspace-drawer`. |
| `<= 734px` | Replace the persistent tree with a full-width labelled file selector; the editor fills the content width and review remains in `workspace-drawer`. |
| `<= 640px` | Use file, edit, and review task tabs; show one task panel at a time without destroying the current file or unsaved editor text. |

At 320 CSS px and at every listed threshold, the page shell and workspace children use `min-width: 0`; tree labels wrap or truncate with an accessible full name, while code, diff, release diagnostics, and the timeline retain horizontal scrolling inside their own labelled panes only. The confirmation modal stays within the viewport without page-level horizontal overflow; timeline rows stack their timestamp beneath the label below 480px. Drawer opening locks background interaction, and task-tab switching preserves visible dirty state and keyboard focus order.

### Footer

**`footer`** — Background `{colors.canvas-parchment}` (#f5f5f7), text `{colors.ink-muted-80}`. Link columns in `{typography.dense-link}` (17px / 400 / 2.41 line-height — the relaxed leading is what makes the dense columns scannable). Column headings in `{typography.caption-strong}` (14px / 600). Legal row at the very bottom in `{typography.fine-print}` (12px / 400) with `{colors.ink-muted-48}` text. Vertical padding 64px.

## Do's and Don'ts

### Do
- Use `{colors.primary}` (Action Blue #0066cc) for every interactive element — links, pill CTAs, focus signals — and nothing else. The single accent is non-negotiable.
- Use semantic status foreground/surface pairs only for health and outcomes, always with visible status text plus the prescribed icon/shape.
- Bind each help and error message through `aria-describedby`, localize `aria-live` to the smallest asynchronous region, and keep every operational control at least 44 × 44px.
- Keep code text selectable and keyboard-scrollable inside `{component.read-only-code-viewer}`; constrain overflow to the viewer rather than the page.
- Set headlines in `{typography.hero-display}` or `{typography.display-lg}` with negative letter-spacing (`-0.28 → -0.374px`) to get the signature "Apple tight" cadence.
- Run body copy at `{typography.body}` (17px / 400 / 1.47 / -0.374px) — not 16px. The extra pixel defines the brand's reading pace.
- Alternate `{component.product-tile-light}` (or parchment) and `{component.product-tile-dark}` for full-bleed section rhythm. The color change IS the divider.
- Reserve `{rounded.pill}` for the primary blue CTA and any other element that should read as an "action" (configurator chips, search input, sticky bar CTA).
- Apply the single product-shadow (`rgba(0, 0, 0, 0.22) 3px 5px 30px`) only to product renders resting on a surface — never on cards, buttons, or text.
- Use `transform: scale(0.95)` as the active/press state on every button — it's the system-wide micro-interaction.
- Keep the global nav `{colors.surface-black}` (true black) — it's the only place pure black appears on most pages.

### Don't
- Don't introduce a second accent color; every "click me" signal is `{colors.primary}` (Action Blue).
- Don't use Success, Warning, Error, or Unknown colors for actions, selected navigation, focus, categories, or decoration.
- Don't communicate status, validation, disabled, or loading state through color or opacity alone.
- Don't make the page shell a live region, and don't announce polling noise or every metric tick.
- Don't turn the read-only code viewer into an editor-shaped widget or let long code create horizontal page scrolling.
- Don't add shadows to cards, buttons, or text — shadow is reserved for product imagery.
- Don't use gradients as decorative backgrounds; atmosphere comes from photography.
- Don't set body copy at weight 500 — Apple's ladder is 300 / 400 / 600 / 700, with 500 deliberately absent. Body is always 400; strong inline is 600; display is 600.
- Don't round full-bleed tiles — tiles are rectangular and edge-to-edge; the color change is the divider.
- Don't tighten line-height below 1.47 for body copy — the editorial leading is part of the brand.
- Don't mix radii grammars — use `{rounded.sm}` for compact utility, `{rounded.lg}` for utility cards, `{rounded.pill}` for pills, and nothing in between (except the rare `{rounded.md}` Pearl Button).
- Don't use `{colors.primary-on-dark}` (Sky Link Blue) on light surfaces — it's the dark-tile-only variant. Action Blue is for light surfaces.

## Responsive Behavior

### Breakpoints

| Name | Width | Key Changes |
|---|---|---|
| Small phone | ≤ 419px | Single-column tiles; sub-nav collapses to category name + primary CTA only; hero typography drops to 28px |
| Phone | 420–640px | Single-column stack; product renders scale to 80% of tile width; hero h1 drops to 34px |
| Large phone | 641–734px | Tiles transition to tighter padding (48px vertical vs 80px); fine-print wraps |
| Tablet portrait | 735–833px | Global nav collapses to hamburger; sub-nav hides category chips, keeps primary CTA |
| Tablet landscape | 834–1023px | Global nav returns fully expanded; 3-column utility grids become 2-column |
| Small desktop | 1024–1068px | Product tiles use 2/3 width with margin gutters; hero h1 stays at 40px |
| Desktop | 1069–1440px | Full layout; 4–5 column store grids; 1440px content max |
| Wide desktop | ≥ 1441px | Content locks at 1440px, margins absorb extra width |

The structural breakpoints that matter for agents: 1440px (content lock), 1068px (small-desktop), 833px (tablet landscape switch), 734px (tablet portrait), 640px (phone), 480px (small phone).

### Touch Targets
- Minimum 44 × 44px. `{component.button-primary}` lands at ~44 × 100px (with the full-pill radius making the visible hit area more generous than the label suggests).
- `{component.button-icon-circular}` is exactly 44 × 44px.
- Compact utility buttons and global-nav links may keep their smaller visible glyph or capsule, but their interactive hit area must expand to at least 44 × 44px. The mobile hamburger replaces the full link row at ≤ 833px.

### Collapsing Strategy
- **Global nav**: full horizontal link row on desktop → collapses to Apple logo + hamburger + bag icon at ≤ 833px.
- **Sub-nav**: category name + inline links + primary CTA → category name + primary CTA only at mobile; inline links move into a hamburger tray.
- **Product tiles**: stack from 2-column to 1-column at 834px; vertical padding tightens from 80px → 48px at small-phone.
- **Utility grids** (store, accessories): 5-col → 4-col (1440px) → 3-col (1068px) → 2-col (834px) → 1-col (640px).
- **Hero typography**: `{typography.hero-display}` (56px) → `{typography.display-lg}` (40px) at 1068px → 34px at 640px → 28px at 419px.

### Operational Surfaces

The following rules use inclusive `max-width` thresholds. At every width, page-shell and main-content children use `min-width: 0`; wrapping and component-level scrolling solve overflow. Do not hide page overflow to mask an oversized child.

| Threshold | Metric grid | Navigation | Effective configuration |
|---|---|---|---|
| > 833px | Three-column dashboard baseline; cards may expand evenly within the content max | Full global and operational section links remain visible | Two columns: bounded file/order navigator + one code viewer |
| ≤ 833px | Collapse to two equal columns | Global nav uses logo + hamburger + utility icon; operational section links move into one labelled menu button | Keep two columns only while each can retain its minimum readable width; both children set `min-width: 0` |
| ≤ 734px | Remain two columns | The section menu stays available from the 44 × 44px minimum menu button; no navigation action is hidden without an equivalent menu item | Replace the persistent navigator column with a full-width labelled file selector; render one selected viewer below it |
| ≤ 640px | Collapse to one column | Page title and the menu button remain in the nav row; secondary actions wrap into a toolbar below | Selector and viewer fill one column; viewer header actions wrap without reducing any target below 44 × 44px |
| ≤ 480px | Stay one column with tighter outer gutters; card padding remains 24px | Keep only the page title, menu, and essential session action in the top row; other actions live in the labelled menu | Stack filename metadata and wrap control; wrapped code reflows, while unwrapped code scrolls horizontally inside the viewer only |

There must be no horizontal page overflow at 833, 734, 640, or 480px. Code is the bounded exception: when line wrapping is off, its own focusable container may scroll horizontally without moving the page.

### Image Behavior
- All product imagery uses responsive `srcset` with breakpoint-matched crops.
- Hero photography may switch art direction at mobile (e.g., the environment page's vista crops to a taller aspect ratio on mobile, framing the subject differently).
- Product renders maintain their 1:1 or 4:3 aspect ratios across breakpoints; only scale changes.
- Lazy-loading is default; the above-fold hero loads eagerly.

## Accessibility Acceptance

Operational primitives must meet **WCAG 2.2 AA** before implementation is accepted:

- Normal text and its surface meet at least 4.5:1 contrast; large text meets 3:1; focus indicators and essential component boundaries meet 3:1 against adjacent colors. Verify each documented status foreground/surface pair as used, not as isolated swatches.
- Every status, validation error, disabled state, and loading state has a non-color cue and an accessible text equivalent. Keyboard focus is visible, ordered, and never trapped.
- All interactive controls have at least a 44 × 44px target, including navigation menus and the code-viewer wrap control.
- At 200% zoom and at 400% reflow (320 CSS px equivalent), content remains operable without two-dimensional page scrolling. Bounded configuration content may scroll inside the labelled code viewer when wrapping is off.
- Acceptance combines automated contrast/semantics checks with keyboard-only, screen-reader, zoom, and 833/734/640/480px viewport checks. A screenshot comparison alone is insufficient.

## Iteration Guide

1. Focus on ONE component at a time. Reference its YAML key directly (`{component.product-tile-dark}`, `{component.search-input}`).
2. Variants of an existing component (`-active`, `-focus`, `-2`, `-3`) live as separate entries in `components:`.
3. Use `{token.refs}` everywhere — never inline hex.
4. Never document hover. Default and Active/Pressed states only.
5. Display headlines stay SF Pro Display 600 with negative letter-spacing. Body stays SF Pro Text 400 at 17px. The boundary is unbreakable.
6. The single drop-shadow (`rgba(0, 0, 0, 0.22) 3px 5px 30px`) is reserved for product photography only.
7. When in doubt about emphasis: alternate surface (light → dark tile) before adding chrome.

## Known Gaps

- The v0.1 read-only configuration page does not use editable workspace controls. The v0.2.1 Configuration Workspace section above defines the compatible tree, editor, diff, drawer, modal, toast, and state contracts that must be used before those controls ship.
- The homepage's embedded video/player frame uses `{colors.surface-black}`; interior player controls are not documented (they're a platform widget, not a web-design token).
- Some component imagery is dynamic (rotating product hero) and its specific copy varies per surface — component specs name the structure, not the rotating content.
- Dark-mode counterparts for store and accessories utility cards were not surfaced on the analyzed pages; the system documented is the daytime/light-dominant variant Apple ships by default.
- Atmospheric photography (environment page mountain vista) is a content asset, not a design token; the documented `{component.environment-quote-card}` describes the structural surface only.
- The exact backdrop-filter blur radius on `{component.sub-nav-frosted}` and `{component.floating-sticky-bar}` is platform-dependent; production CSS uses `saturate(180%) blur(20px)` as a typical baseline but the value isn't formalized as a token.
