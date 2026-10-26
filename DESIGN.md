---
name: Mestre panel
description: Pergaminho, a warm paper and ink panel for people who do not know RPG.
colors:
  paper: "#f4ecdc"
  surface: "#fbf6ea"
  ink: "#2a2118"
  muted: "#6a5b4b"
  line: "#d9ccb2"
  accent: "#8a3b1e"
  accent-strong: "#6e2d15"
  accent-ink: "#fff7ea"
  accent-wash: "#ecdcc4"
  signal: "#2f6b4f"
  danger: "#a3281c"
  danger-wash: "#f6ddd6"
  warn: "#f1b04c"
typography:
  display:
    fontFamily: "Fraunces Variable, Georgia, serif"
    fontWeight: 500
    lineHeight: 1.15
    letterSpacing: "-0.01em"
  body:
    fontFamily: "Newsreader Variable, Georgia, serif"
    fontSize: "17px"
    lineHeight: 1.5
rounded:
  field: "3px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-ink}"
    rounded: "{rounded.field}"
    height: "44px"
---

Derived from `web/src/index.css` (the `@theme` block is the source of the values) and kept by hand in `docs/painel/design.md`; this file exists for the Impeccable tool. When they differ, the document and the token file win.

## Overview

A well kept adventure notebook that is also a calm work tool. Warm paper, dark ink, one brick-red accent for the main action and for focus. The RPG shows in the words (Guild, Citadel, Tower) and in the serif headings, never in ornament. Light theme only.

## Colors

Paper is the page, surface is the card and the bars. Ink for text, muted for support text (5.5:1 on paper). Accent only for the primary action, links and focus; signal (green) only for "all fine"; danger only for errors and destructive actions; warn (amber) only for the reconnecting band.

## Typography

Fraunces for headings, Newsreader for text and controls, both self-hosted. Interface text starts at 17 px; support text is never below 14 px.

## Layout

Entrance screens: one centered card on paper. Inside: top bar, side nav (a scrolling row on a phone) and a content area. Touch targets are at least 44 px.

## Elevation & Depth

Thin lines instead of heavy shadows. Shadow only for what floats: the entrance card, the help balloon and menus.

## Shapes

Barely rounded corners (3 px); the brand mark and the avatar are circles.

## Components

Buttons (primary, secondary, quiet, danger), labelled fields with the error under them, the Term "?" help balloon, the PIN field that appears after a sensitive button, the amber reconnecting band.

## Do's and Don'ts

- Do take every color, font and shadow from a token.
- Do show the real-world phrase with every term help.
- Don't use a color alone to carry meaning.
- Don't write help text in code: it comes from the glossary.
