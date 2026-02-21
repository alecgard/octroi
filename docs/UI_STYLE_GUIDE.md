# Octroi UI Style Guide

The admin dashboard is a single embedded HTML page (`internal/ui/index.html`) with no build step, no framework, and no external stylesheets. All CSS and JS is inline.

## Design Language

Monospace-first, terminal-inspired. Sharp edges (no border-radius on primary elements). Neutral palette with colour used sparingly for semantics (danger, success, warning). Dense but readable.

## Colour Tokens

| Token | Value | Usage |
|-------|-------|-------|
| `--bg` | `#ffffff` | Page background, input backgrounds |
| `--surface` | `#f5f5f0` | Card/section backgrounds, topbar, hover states |
| `--surface2` | `#eaeae5` | Tertiary surface, row borders |
| `--border` | `#d0d0c8` | All borders, dividers |
| `--text` | `#1a1a1a` | Primary text, headings, accent |
| `--text2` | `#707068` | Secondary text, labels, placeholders |
| `--accent` | `#1a1a1a` | Primary buttons, active states |
| `--accent-hover` | `#444` | Primary button hover |
| `--danger` | `#c33` | Delete/destructive actions |
| `--success` | `#2a7` | Success banners, live indicator |
| `--warn` | `#a80` | Admin badges, warning states |

## Typography

- **Font stack**: `'SF Mono', 'Cascadia Code', 'Fira Code', 'JetBrains Mono', 'Consolas', monospace`
- **Base size**: 12px, line-height 1.5
- **Labels/headers**: 10-11px, `font-weight: 600-700`, `text-transform: uppercase`, `letter-spacing: 0.5-1px`
- **Tab titles**: 11px uppercase, 2px underline on active
- **Section headings** (within tabs): 14px, `font-weight: 600`, sentence case
- **Values/data**: 12px normal weight

## Spacing

- **Page padding**: 16px, `max-width: 1200px`
- **Component gap**: 8-12px (flex gap)
- **Section margin-bottom**: 12px
- **Form group margin-bottom**: 10px
- **Modal padding**: 16px

## Buttons

| Variant | Background | Text | Border | Usage |
|---------|-----------|------|--------|-------|
| `.btn-primary` | `--accent` | `#fff` | `--accent` | Create, Save, Connect |
| `.btn-ghost` | transparent | `--text2` | `--border` | Cancel, secondary actions |
| `.btn-danger` | `--bg` | `--danger` | `--danger` | Inline Delete/Remove |
| `.btn-danger-fill` | `#c33` | `#fff` | `#c33` | Confirm dialog destructive action |
| `.btn-warn` | `--bg` | `--warn` | `--warn` | Regenerate Key |
| `.btn-sm` | (modifier) | | | Smaller padding, used in tables/lists |

All buttons: `font-family: var(--font)`, `font-size: 11px`, `padding: 4px 10px`.

## Tables

- `table-layout: fixed` for most tables (agents, users)
- `table-layout: auto` for content-heavy tables (tools, usage transactions)
- Last column: `width: 120px; text-align: right` for action buttons
- Header: 10px uppercase, `--text2`, 2px bottom border
- Rows: 5px 8px padding, 1px bottom border, hover highlight

## Tooltips

**Always use the app tooltip pattern, never the native browser `title` attribute.** The app tooltip provides a consistent dark-on-light style that matches our design.

### Pattern

```html
<span class="has-tooltip">
  Visible text
  <span class="tip">Full tooltip content</span>
</span>
```

### Styling

```css
.has-tooltip { position: relative; cursor: help; }
.has-tooltip .tip {
  display: none; position: absolute; bottom: 100%; left: 50%; transform: translateX(-50%);
  background: var(--text); color: var(--bg); padding: 4px 8px; font-size: 10px;
  font-weight: 400; text-transform: none; letter-spacing: 0; white-space: nowrap;
  z-index: 10; margin-bottom: 4px;
}
.has-tooltip:hover .tip { display: block; }
```

### Variations

- **Centred** (default): `left: 50%; transform: translateX(-50%)` — used on table headers
- **Left-aligned**: `left: 0; transform: none` — used on truncated text where content is left-aligned
- **Cursor**: use `cursor: help` for informational tooltips on headers, `cursor: default` for truncated content tooltips

### When to use

- Truncated text (ellipsis) — tooltip shows full content
- Table header explanations (e.g. "Rate Limit" header explaining scope)
- Disabled buttons — tooltip explains why disabled
  - Note: disabled buttons currently use `title` attr because `:hover` doesn't fire on disabled elements; this is the one acceptable exception

## Modals

- Overlay: `rgba(0,0,0,0.3)`, fixed position, flex centered
- Modal box: `max-width: 520px`, `max-height: calc(100vh - 120px)`, scroll-y
- Confirm dialog: `max-width: 360px`
- Close on overlay click and Cancel button
- Form labels: 10px uppercase `--text2`
- Form inputs: full width, 12px, `--border` border, `--text` on focus
- `.form-row`: 2-column grid with 10px gap
- Actions: flex row, right-aligned, 6px gap

## Cards

- Grid: `repeat(auto-fill, minmax(180px, 1fr))`, 12px gap
- Card: `--surface` background, `--border` border, 12px padding
- Label: 10px uppercase `--text2`
- Value: 20px bold

## Badges

`.role-badge`: inline-block, 9px uppercase bold, 1px border.

- Default: `--border` border, `--text2` colour
- `.admin`: `--warn` border and colour

**Fixed-width badges** (e.g. team member lists): set explicit `width` and `text-align: center` so badges align across rows.

## Team Member Lists

Each user row is a flex container:
```
[name <email>···fixed width···] [ROLE···fixed width···] [Remove]
```

- Name+email: fixed width (280px), `overflow: hidden; text-overflow: ellipsis; white-space: nowrap`
- App tooltip (`.tip`) shows full untruncated name and email on hover
- Role badge: fixed width (52px), centred
- Remove button: `margin-left: 8px`
- No org-level badges in team context (not relevant)

## Permissions & Visibility

**Never hide actions based on permissions — disable them instead.** Every action button (Create, Edit, Delete, Add Member, Remove) is always visible. If the user lacks permission, the button is `disabled` with a tooltip explaining why (e.g. "Org admin only").

The only exception is UX simplification unrelated to permissions (e.g. hiding team input when auto-assigned).

## Toast Notifications

Fixed bottom-centre, dark background (`--text`), white text, 11px. Fade in/out with 3s default duration.

## Live Mode

- Toggle: segmented button (Live / Historical)
- Green pulsing dot indicates live polling
- Yellow dot (shift held or chart paused) indicates paused state
- 3-second polling interval
- New transaction rows animate with green fade-in

## Filter Dropdowns (Multi-select)

Custom component with:
- Search input at top
- Checkbox-style options
- Tag display for selected items (max 1 tag + "+N" overflow)
- Clear button (x) on hover when selections active
- Shift-click on transaction table rows for batch filter editing

## Charts

SVG line chart with:
- Y-axis gridlines (dashed)
- Hover zones per bucket with tooltip
- Legend with click-to-filter and hover-to-highlight
- Colour palette: `['#1a1a1a','#2a7','#a80','#c33','#57a','#7a5','#a57','#5a7']`
- Auto bucket size based on time range, manual override available

## Empty States

Centred text, `--text2`, 32px padding: "No agents created yet." etc.

## Login Screen

Centred at 80vh, 320px box, `--surface` background. Form with Email + Password + Connect button. Error shown inline in `--danger`.

## Architecture Notes

- No build step — raw HTML/CSS/JS
- Embedded via `go:embed`
- Vanilla JS, no framework
- All state in module-level variables
- Hash-based tab routing (`#agents`, `#tools`, `#usage`, `#teams`, `#users`)
- API calls via `fetch` with Bearer token from localStorage
- 401 responses trigger automatic session clear and redirect to login
