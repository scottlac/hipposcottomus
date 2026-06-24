# 🏁 CI360 Bracket Board

A local, **offline**, browser-based **single-elimination tournament manager** with a
live projector bracket — built for an in-person Mario Kart World event run from one
laptop. No internet, no backend, no database. One operator drives a **control panel**
on the laptop screen; an **extended display** (projector) shows a big, legible **live
bracket** for the room.

> Original racing/arcade look — checkered-flag motifs, podium gold/silver/bronze, and
> smooth advance animations. No Nintendo/Mario artwork, characters, or fonts.

---

## Quick start

```bash
cd tournament
npm install
npm run dev
# → http://localhost:5173
```

Open **`http://localhost:5173`** on the laptop's main screen. That's the operator
**control panel** (route `#/control`). The projector view lives at
**`http://localhost:5173/#/display`**.

Everything is saved to the browser's `localStorage` and mirrored live between the two
windows, so a refresh or an accidental window close never loses the tournament.

---

## Two-window setup (laptop + projector)

The control panel and the display are two windows of the **same browser** at the
**same address**, so they automatically stay in sync (via `BroadcastChannel`, with a
`localStorage` fallback).

1. **Extend your desktop** (don't mirror), so the projector is a separate screen:
   - **Windows:** press `Win`+`P` → choose **Extend**.
   - **macOS:** System Settings → **Displays** → select the projector → **Use as:
     Extended display** (make sure *Mirror Displays* is **off**).
   - **Linux (GNOME):** Settings → Displays → **Join Displays**.
2. On the control panel, click **🖥 Open display**. A second window opens at
   `#/display`. (Or open a new browser window yourself and go to
   `http://localhost:5173/#/display`.)
3. **Drag that display window onto the projector screen.**
4. **Fullscreen it:**
   - **Windows / Linux (Chrome/Edge):** press `F11`.
   - **macOS (Chrome):** `Ctrl`+`Cmd`+`F`, or View → Enter Full Screen.
   - Or use the browser menu → **Full screen / Enter Presentation**.

The display auto-scales the whole bracket to fit the screen (no scrolling) at ~24
players and degrades gracefully beyond that.

---

## Running the event

1. **Roster** — paste names (one per line) or add them one at a time. Walk-ups can be
   added any time before you lock. Reorder with ↑/↓, rename inline, or **🎲 Shuffle
   seeds** for a random draw.
2. **Generate Round 1** — auto-suggests races of 4. Uneven counts split into smaller
   races (3 or 2) so nobody races alone; a lone leftover becomes a **bye** that
   advances directly. **Drag racers between races** to adjust freely.
3. **Advancement** — set how many advance per race (default **2**); override per round
   if needed. A single-race round automatically crowns one champion.
4. **🏁 Lock & start** — freezes the roster and Round 1 and starts the tournament.
5. **Run races** — load a pending race onto **Station A** or **Station B** (two can run
   at once). Enter the result by tapping racers **in finishing order** (or press number
   keys `1`–`9`, `⌫` to undo, `Enter` to save). The top N advance and flow into the
   next round; the rest are eliminated. The next round appears once **all** its feeder
   races finish.
6. **Fix mistakes** — single elim is unforgiving, so any completed race can be
   **✎ Edited** or **↺ Re-opened**. Downstream rounds **re-resolve automatically**: if
   an edit changes who advances, the affected later races become un-entered again
   (and if you revert the edit, the old results come right back). **Undo** (button or
   `Ctrl`/`Cmd`+`Z`) reverts the last action.
7. **Export** — **JSON** (full structured snapshot) or **CSV** (standings) at any time.
   **Reset** clears everything.

A compact **live bracket preview** sits inside the control panel so you don't have to
look up at the projector to track state.

### Keyboard shortcuts

| Key | Action |
|---|---|
| `1`–`9` | Place that racer next in finishing order (in the active result entry) |
| `⌫` Backspace | Remove the last placement |
| `Enter` | Save the result (once everyone is placed) |
| `Esc` | Cancel an edit |
| `Ctrl`/`Cmd`+`Z` | Undo last action |

---

## Offline / production build

The dev server is fully offline already. For a standalone build:

```bash
npm run build      # outputs to dist/
npm run preview    # serves the built app locally
```

`dist/` is self-contained and uses **relative asset paths** + **hash routing**, so you
can serve it from any static file server (or even open `dist/index.html` directly in
Chrome) and the `#/control` / `#/display` routes still work. Both windows must use the
**same origin** to share state.

---

## How it stays correct (notes for the curious)

The app keeps one small, serializable **input** object as the single source of truth
(roster, Round 1 grouping, per-round advance counts, recorded results, station
assignments). The full bracket is a **pure function** of that input
(`src/bracket.ts → resolve()`), so editing or undoing a past result simply re-derives
every later round — there is no fragile downstream patching.

The trick: results are keyed by the **set of players** in a race, not by a bracket
position. Change who advances upstream and the downstream races become different player
sets, so they're naturally "un-entered" until you record them — and reverting restores
the originals.

A clean seam is left for a future **finale race mode** (a single climactic race among
the finalists): the resolver already treats a single-race round as the Final, so a
finale mode can hang off that without reworking the engine.

## Tech

React + Vite + TypeScript, Tailwind CSS v4, `BroadcastChannel` + `localStorage`. No
backend, no accounts, no game integration — all results are entered by the operator.
