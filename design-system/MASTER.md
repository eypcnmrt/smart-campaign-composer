## Design System: Smart Campaign Composer

### Design Dials
- **Variance:** 3/10 — Centered / Minimal
- **Motion:** 2/10 — Subtle
- **Density:** 7/10 — Standard

### Pattern
- **Name:** Enterprise Gateway
- **Conversion Focus:** Path selection (I am a...). Mega menu navigation. Trust signals prominent. Provide pause/stop for video and rotating logos; stop on focus and reduced motion. Logo carousel controls must be keyboard operable; pause moving media offscreen/hidden and render a static final state under reduced motion.
- **CTA Placement:** Contact Sales (Primary) + Login (Secondary)
- **Color Strategy:** Corporate: Navy/Grey. High integrity. Conservative accents.
- **Sections:** Hero (Video/Mission) > Solutions by Industry > Solutions by Role > Client Logos > Contact Sales

### Style
- **Name:** Minimalism & Swiss Style
- **Mode Support:** Light supported | Dark supported
- **Keywords:** Clean, simple, spacious, functional, white space, high contrast, geometric, sans-serif, grid-based, essential
- **Best For:** Enterprise apps, dashboards, documentation sites, SaaS platforms, professional tools
- **Performance:** cost:low|drivers:none | **Accessibility:** risk:low|requires:contrast-text-4.5,keyboard,visible-focus,reduced-motion

### Colors
| Role | Hex | CSS Variable |
|------|-----|--------------|
| Primary | `#1E40AF` | `--color-primary` |
| On Primary | `#FFFFFF` | `--color-on-primary` |
| Secondary | `#3B82F6` | `--color-secondary` |
| On Secondary | `#000000` | `--color-on-secondary` |
| Accent/CTA | `#D97706` | `--color-accent` |
| On Accent/CTA | `#000000` | `--color-on-accent` |
| Background | `#F8FAFC` | `--color-background` |
| Foreground | `#1E3A8A` | `--color-foreground` |
| Card | `#FFFFFF` | `--color-card` |
| Card Foreground | `#1E3A8A` | `--color-card-foreground` |
| Muted | `#E9EEF6` | `--color-muted` |
| Muted Foreground | `#475569` | `--color-muted-foreground` |
| Border | `#DBEAFE` | `--color-border` |
| Destructive | `#DC2626` | `--color-destructive` |
| On Destructive | `#FFFFFF` | `--color-on-destructive` |
| Ring | `#1E40AF` | `--color-ring` |

*Notes: Blue data + amber highlights [Accent adjusted from #F59E0B]*

### Typography
- **Heading:** Fira Code
- **Body:** Fira Sans
- **Mood:** dashboard, data, analytics, code, technical, precise
- **Best For:** Dashboards, analytics, data visualization, admin panels
- **Google Fonts:** https://fonts.googleapis.com/css2?family=Fira+Code:wght@400;500;600;700&family=Fira+Sans:wght@300;400;500;600;700&display=swap
- **CSS Import:**
```css
@import url('https://fonts.googleapis.com/css2?family=Fira+Code:wght@400;500;600;700&family=Fira+Sans:wght@300;400;500;600;700&display=swap');
```

### Key Effects
Subtle hover (200-250ms), smooth transitions, sharp shadows if any, clear type hierarchy, fast loading

### Motion
**Scroll Reveal** (Subtle) — Trigger: scroll (viewport enter) | Duration: 300-400ms | Easing: `power1.out`
```js
gsap.from(el, { opacity: 0, y: 12, duration: 0.35, ease: 'power1.out', scrollTrigger: { trigger: el, start: 'top 90%', toggleActions: 'play none none reverse' } });
```
*Framework notes: Requires the ScrollTrigger plugin registered once via gsap.registerPlugin(ScrollTrigger); Use matchMedia('(prefers-reduced-motion: reduce)') to skip non-essential motion and render the final state immediately*
- ✅ Keep the y offset small (8-16px) so it reads as a fade, not a slide
- ❌ Don't reveal below-the-fold content needed for SEO/crawlers as invisible-by-default without a no-JS fallback

### Avoid (Anti-patterns)
- Ornate design
- No filtering

### Pre-Delivery Checklist
- [ ] No emojis as icons (use SVG: Heroicons/Lucide)
- [ ] cursor-pointer on all clickable elements
- [ ] Hover states with smooth transitions (150-300ms)
- [ ] Light mode: text contrast 4.5:1 minimum
- [ ] Focus states visible for keyboard nav
- [ ] prefers-reduced-motion respected
- [ ] Responsive: 375px, 768px, 1024px, 1440px


---

## Project decisions (added by hand; everything above is the unmodified search output)

The search was run with the query "marketing campaign dashboard internal tool data-dense professional" and the dials
density 7, variance 3, motion 2. A first, broader query ("internal marketing campaign composer tool") returned a landing-page
pattern and a pink brutalist style, which do not fit, so it was discarded.

### Taken from the result
- **Style: Minimalism & Swiss.** Functional, grid-based, high contrast, light mode, sharp structure, borders instead of shadows.
- **Dials:** moderate density, low variance, subtle motion; everything honours `prefers-reduced-motion`.
- **Checklist:** visible focus, 4.5:1 text contrast, no emoji as icons, responsive at 375/768/1024/1440, cursor on clickable things.

### Changed, and why
| Result | Used instead | Reason |
|---|---|---|
| Pattern "Enterprise Gateway" (hero, client logos, Contact Sales) | none | A landing-page pattern; this is a daily-use internal tool, not a site. |
| Navy primary `#1E40AF` + amber accent `#D97706` | Ink green `#0E6B58` on cool paper `#F3F5F2`, one highlighter yellow `#F3E55B` | The suggested pair is the stock enterprise-SaaS look. The yellow has a job (below) instead of being decoration. |
| Fira Code / Fira Sans from Google Fonts | Schibsted Grotesk (headings, numbers) + Source Sans 3 (text), self-hosted with `@fontsource-variable` | No monospace for data labels. Both families cover Turkish (the latin-ext subset has ğ ş İ; ı is in latin). No CDN: the CSP allows fonts from `self` only. |
| Entrance animation (scroll reveal) | none | Nothing animates on its own; motion only answers an action (opening a panel, a step change). |

### Tokens (the CSS in `web/src/styles/tokens.css` is the source; this table is the summary)
| Token | Value | Use |
|---|---|---|
| paper / surface / sunken | `#F3F5F2` / `#FFFFFF` / `#E7ECE8` | page / panels and inputs / wells and read-only previews |
| ink / ink-soft | `#14302A` / `#44605A` | text / secondary text (6.3:1 on paper) |
| brand / brand-strong / tint | `#0E6B58` / `#0A5445` / `#DCEFE8` | actions and links / hover / selected |
| mark | `#F3E55B` | the highlighter: personalization variables, the live-writing caret, the "Mock LLM" badge |
| success (+ bg, border) | `#2B6A17` / `#E4F2D6` / `#4C9A2A` | sent, completed, ok: see "State colours" |
| warning (+ bg, border) | `#7F4100` / `#FFEACB` / `#BF6A0C` | matches_everyone, unsupported_criteria, provider unreachable, completed with failures |
| danger (+ bg, border) | `#A8261D` / `#FBE3E0` / `#C93A30` | no_recipients, failed, worker down, request errors |
| line / line-strong | `#CBD5CE` / `#74897F` | decorative rules / input and control borders (3:1 or better) |
| focus | `#0B57D0` | 3 px ring, offset 2 px, on every focusable element |
| font-mono | `ui-monospace, 'Cascadia Mono', 'Segoe UI Mono', SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace` | raw JSON rule panel and code; the system's fonts, no font package |

Contrast was computed, not guessed, and `web/src/styles/tokens.test.ts` re-checks it from the CSS on every test run: every text pair
is at least 5.4:1, control borders at least 3.4:1, the focus ring at least 5.3:1. Editing a colour below those values fails the tests.

### State colours
Four things look alike if they share a hue, so each has its own, and the tests keep them apart:

| Meaning | Colour | Where it may appear |
|---|---|---|
| Act, choose, navigate | action teal (hue 168) | buttons, links, the selected step, the info note |
| Done, delivered (ok, sent, completed) | success leaf green (hue 106) | only as a tinted badge or note with a check icon, never on a control, never as a link |
| Look before you go on | warning amber-orange (hue 31) | matches_everyone, unsupported_criteria, provider_unreachable, retrying, completed_with_failures |
| Something is wrong or blocked | danger red (hue 4) | no_recipients, failed, worker_down, request errors, invalid fields |
| Not final text | highlighter yellow (hue 54) | personalization variables, live-writing caret, "Mock LLM". Never used for a warning |

- **Never colour alone.** A state is an icon plus a word, always: circle with a check (success), triangle (warning), octagon with a
  cross (danger), circle with an i (info), clock (waiting). `StatusBadge` takes a required label, and `StatusNote` adds a hidden word
  ("Hata:", "Uyarı:") for screen readers. A test checks that every tone draws a different shape.
- Text and icons take the state colour on its tint, on white, on the page and on the sunken well, all at 5.5:1 or more.
- Messages that appear after an action are live regions (`role="alert"` for danger, `role="status"` otherwise); messages that are
  already on the page when it opens are not.

### Type
Schibsted Grotesk 600-700 for page titles, step titles and big numbers (tight tracking, balanced wrapping); Source Sans 3 for
everything else at 16 px / 1.5, 14 px for dense data. Line length stays under 75 characters.
`lang="tr"` is set on the document, so case conversion follows Turkish rules (i becomes İ).

**Numbers that change** (character counters, audience counts, dashboard figures, rates) take the `.num` class, which is
`font-variant-numeric: tabular-nums`, so digits do not jump while a page polls. Checked in the font files and in a browser (digit
strings such as 0000000000 and 1111111111 measured at weights 400, 600 and 700): Source Sans 3 has tabular digits by default (the
latin subset has no `tnum` feature, so the class changes nothing there and is harmless), and Schibsted Grotesk has proportional
digits by default and tabular ones with `tnum` (203.1 px for every digit string at 32 px). Both fonts support it, but Schibsted's
tabular digits give the separator a full digit cell ("3 . 412", "1 . 111"), which looks poor in a large figure. So `.num` also sets
the body font: counters, audience counts and dashboard figures are Source Sans 3 (600-700 for big ones), never Schibsted.

**Raw JSON** (the rule panel) and code use `--font-mono`, the system monospace stack; no monospace font package is added.

### Layout
Slim header (56 px): wordmark, Kampanyalar, Yeni kampanya, and on the right the model: "Mock LLM" badge when no key is set, plus the model name.
Campaign pages: a step rail on the left (Brief, Kitle, İçerik, Onay, Gönderim) as a vertical line with a mark per step, everything
left-aligned. Under 960 px the rail becomes a compact horizontal stepper above the content.

**Two measures** (`PageColumn`): brief, audience, content and approval stay in a narrow column of 760 px (forms and reading, lines
under 75 characters). The campaign list and the delivery dashboard use a wide column of 1120 px, so a table and two channels side by
side are not squeezed. The page itself is 1120 px plus its gutters, so the header lines up with the wide column.
Panels are ruled sections with a 1 px border and a 6 px radius, not a grid of identical cards; inputs and chips have a 3 px radius.

### What makes it this product's
The highlighter. A personalization variable such as `{{first_name}}` is marked like text in a printed proof; the model that is
still writing, and a model that is not real, get the same mark. Yellow means "this is not final text". It is the only loud colour.
