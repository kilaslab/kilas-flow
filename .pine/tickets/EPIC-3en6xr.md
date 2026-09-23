---
id: EPIC-3en6xr
title: "Frontend visual revamp: compact, smooth, distinctive (\"Kunyit\")"
status: todo
priority: high
labels:
    - frontend
    - design-system
    - visual-revamp
created: "2026-09-23T03:05:15Z"
updated: "2026-09-23T03:05:15Z"
---

# Description

The owner said the frontend "doesn't feel smooth, especially the colours", and asked for a revamp that is as **compact and smooth as n8n without looking like n8n**. A read-only visual audit on 2026-09-23 measured every surface in both themes at 1440 and 1280 px. It covered computed colours, sizes, radii, contrast, frame timing and layout shift.

**Direction decided by the owner:** A, **"Kunyit"** (turmeric gold on cool slate). The first proposal, "Nila" (indigo), was rejected because it would resemble qonflo.ai. The audit measured qonflo.ai's and n8n's brand hues as exclusion zones. Kunyit sits at least 40° from every n8n hue and at least 108° from every qonflo hue.

Attachments: `frontend-visual-audit.md` (the full audit, including the exclusion zones, the three directions, the draft `app.css` token block, the motion spec and the density spec) and `direction-preview.png` (A Kunyit, B Cetak Biru and C Serai side by side, dark and light).

## Verdict: why it feels "not smooth"

The frame rate is fine; the colour system is the problem. On a 265-node workflow, pan and zoom run at p95 17 ms per frame, with 2 frames over 33 ms, and layout shift is 0.0007 or less on every page. The roughness is visual, and it has five measurable causes:

1. **One hue plays every role.** Every token in `app.css` sits on hue 170: background, card, sidebar, hover surface, border tint, primary, success, ring, chart-1 and `node-core`. `--success` is literally `--primary`, so brand actions, links, selection, focus and "Succeeded" look identical. On `/executions`, 29 of 138 text elements are brand teal `#3bc59e`, plus 12 teal pill fills. The UI has no neutral ground for colour to stand out against, so no single colour means anything. At L 0.17 the chroma of 0.016–0.020 reads as green-black (`#08120e` page, `#101c18` card), which gives the "swampy" cast.
2. **The canvas has no colour grammar.** Node tint, port colour and selection ring all take `definition.iconColor`, a free hex string. There are 24 distinct values in `nodes/*.go`, including `#000000`, `#ef4444` and `#ea4335`, and the two reds collide with the error colour. One 9-node canvas paints 7 tint colours and 25 border colours. Selecting an IF node draws an orange ring, which reads as a warning. Meanwhile the five category tokens `--node-trigger/core/ai/data/imported` exist in `app.css` and are referenced **0 times**.
3. **Loud against quiet, with nothing in between.** The mint primary (L 0.74, C 0.13) glows on near-black. The editor toolbar has two filled primaries side by side ("Add step" and "Execute"). Pastel light-theme stickies (`#fef3c7` and similar) sit on the dark canvas as bright slabs (see BUG-rbask0). The edges use L 0.74 at 8.3:1, so they compete with the node labels.
4. **Too many values.** Across the pages there are 8 computed font sizes (8, 10, 11, 12, 12.8, 13, 14 and 16 px, plus 20 px in source), 4 weights, 7 radii (1, 4, 8, 10, 14, 34 px and pill), 3 focus-ring treatments, 8 button heights (20–40 px), at least 34 background and at least 37 border colours, and 4 easing/duration pairs. The workflows list sets 76% of its text at 11 px. Row heights are 39, 44, 45, 49 and 50 px across five lists, and pages use three content widths (896 px, 1024 px and full).
5. **Hard cuts where motion should explain.** The inspector appears in a single frame (pane 1232→912 px, no transition). The node picker pops in with a `backdrop-blur` over the live canvas and no transition. The editor loads behind a plain "Loading workflow editor…" text. Popovers and menus have `box-shadow: transparent` and an overlay only ΔL 0.045 above the page, so they don't separate from what is beneath them.

In short, KilasFlow is already **denser than n8n**: 68 px nodes against n8n's roughly 100 px, 28 px controls against 32 px, and 11–13 px text against 13–14 px. What it lacks is n8n's *calm*, meaning neutral chrome, one accent spent sparingly, one radius and one type rhythm. The revamp keeps the density, rebuilds the colour system, and gives KilasFlow an identity of its own ("Kunyit", turmeric on cool slate, §6–§7) that stays measurably clear of n8n's coral/orange/pink-red and of qonflo.ai's violet/magenta.

**Calibration note.** The current look (near-black background with one bright mint/teal accent) falls in the most common generated-UI cluster: "near-black with a single neon accent". That is part of why it reads as generic rather than owned.

## Chosen direction: Kunyit

Kunyit fixes the root cause: brand and state have separate hues, and turmeric is used only where you act. It keeps KilasFlow's tighter-than-n8n density (64 px nodes, 28 px controls, 13 px base), and it sits outside both measured exclusion zones:
- The accent (h 95) is 40° or more from every n8n hue and 108° or more from every qonflo.ai hue.
- The neutrals are cool slate (h 235, C ≤ 0.012) rather than purple-black.
- There are no gradients.

It is also ownable in the automation category, where the neighbours are orange (n8n, Zapier), purple (Make, qonflo.ai, Activepieces), green (Pipedream) and blue (Power Automate, Windmill).

Kunyit makes the embed story better too:
- One `--kf-accent-src` recolours every accent step.
- The text on the accent fill flips between ink and white by the host accent's lightness, keeping ≥4.69:1 across 15 sample host accents (tested threshold L 0.60).
- Statuses stay correct whatever accent a host picks.

**Direction contract (for the implementing tickets):**
- **THESIS.** Turmeric shows only where you act. Chrome is cool slate, state has its own hues, and nothing is tinted "for brand".
- **OWN-WORLD.**
  - Slate neutrals at h 235, C ≤ 0.012, in 5 surface steps.
  - Kunyit `oklch(.82 .14 95)` (`#e1c34b`) as the fill of one primary action per view, with ink text.
  - Turmeric (dark) or deep ochre (light) for selection, focus and links.
  - Success 150, warning 60 (moved off amber), danger 25 and info 245.
  - 64 px node tiles with jade, sky, leaf and steel category glyph chips.
  - Atkinson Hyperlegible Next and Mono.
  - Radii of 4, 6 and 10 px.
  - Motion on 120, 180 and 220 ms tokens.
- **REFUSES.**
  - Violet, magenta, pink, coral and orange as identity.
  - Purple-tinted surfaces, and gradients.
  - Brand-tinted surfaces.
  - Per-node arbitrary hex.
  - Success equal to brand, and amber warning next to a turmeric brand.
  - Mono as decoration.
  - Hard-cut panels.

# Goals

- One calm, distinctive visual system, "Kunyit":
  - cool slate chrome (hue 235, low chroma), in five surface steps;
  - one turmeric-gold accent spent only on the primary action, selection, focus and links;
  - warning moved to tangerine, and success, danger and info kept on hues of their own;
  - category colour on node glyphs only.
- n8n-level compactness and smoothness: keep today's density, remove the hard cuts (panel choreography, animated viewport actions, skeletons), and stop the colours competing.
- Every colour is a token, and every token pair passes WCAG AA in both themes, enforced by a lint and a screenshot regression harness. The accent stays out of the qonflo.ai and n8n exclusion zones.
- Dark-first stays: bare `:root` is dark, and light is opt-in.

# Notes

This epic owns the visual system. Behaviour and rendering bugs already filed stay in their own tickets and are referenced from the children: BUG-15st2k (canvas node rendering), BUG-rbask0 (stickies), BUG-bcahaj (editor chrome collisions), FEAT-ktasef (run animation), BUG-namghh (contrast), FEAT-a3dwj2 (theme toggle).

# Attachments

- `frontend-visual-audit.md`: the full audit, with exclusion zones, directions, the draft token block, motion, density and verification tooling
- `direction-preview.png`: Kunyit, Cetak Biru and Serai side by side
