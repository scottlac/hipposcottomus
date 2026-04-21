(() => {
  "use strict";

  const RANKS = ["A", "K", "Q", "J", "T", "9", "8", "7", "6", "5", "4", "3", "2"];
  const displayRank = (r) => (r === "T" ? "10" : r);
  const SUITS = [
    { code: "s", glyph: "♠", name: "Spades",   color: "black" },
    { code: "h", glyph: "♥", name: "Hearts",   color: "red"   },
    { code: "d", glyph: "♦", name: "Diamonds", color: "red"   },
    { code: "c", glyph: "♣", name: "Clubs",    color: "black" },
  ];

  const MAX_HAND = 2;
  const MAX_BOARD = 5;
  const DEBOUNCE_MS = 300;
  const RING_CIRC = 2 * Math.PI * 52; // 326.73...

  const state = {
    // Map from card id ("As", "Kh") to "hand" | "board"
    selections: new Map(),
    numPlayers: 6,
    inFlight: null,
  };

  const el = {
    suits:        document.getElementById("suits"),
    handCount:    document.getElementById("handCount"),
    boardCount:   document.getElementById("boardCount"),
    handDisplay:  document.getElementById("handDisplay"),
    boardDisplay: document.getElementById("boardDisplay"),
    numPlayers:   document.getElementById("numPlayers"),
    clearAll:     document.getElementById("clearAll"),
    resultPrompt: document.getElementById("resultPrompt"),
    resultContent:document.querySelector(".result__content"),
    winPct:       document.getElementById("winPct"),
    ringFg:       document.getElementById("ringFg"),
    spinner:      document.getElementById("spinner"),
    winValue:     document.getElementById("winValue"),
    tieValue:     document.getElementById("tieValue"),
    handDesc:     document.getElementById("handDesc"),
  };

  // ── Picker grid ───────────────────────────────────────────────

  function suitColor(suit) {
    return suit.color === "red" ? "mini-card--red" : "mini-card--black";
  }

  function buildPicker() {
    SUITS.forEach((suit) => {
      const row = document.createElement("div");
      row.className = "suit-row " + (suit.color === "red" ? "suit-row--red" : "suit-row--black");

      const header = document.createElement("div");
      header.className = "suit-row__header";
      header.textContent = suit.glyph + " " + suit.name;
      row.appendChild(header);

      const grid = document.createElement("div");
      grid.className = "suit-row__cards";

      RANKS.forEach((rank) => {
        const id = rank + suit.code;
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "mini-card " + suitColor(suit);
        btn.dataset.cardId = id;

        const r = document.createElement("span");
        r.className = "mini-card__rank";
        r.textContent = displayRank(rank);
        const s = document.createElement("span");
        s.className = "mini-card__suit";
        s.textContent = suit.glyph;

        btn.appendChild(r);
        btn.appendChild(s);
        btn.addEventListener("click", () => onCardTap(id, btn));
        grid.appendChild(btn);
      });

      row.appendChild(grid);
      el.suits.appendChild(row);
    });
  }

  // ── Selection logic ───────────────────────────────────────────

  function countState(target) {
    let n = 0;
    state.selections.forEach((v) => { if (v === target) n++; });
    return n;
  }

  function onCardTap(id, btn) {
    const current = state.selections.get(id);
    if (!current) {
      // Not selected → try to assign to board.
      if (countState("board") >= MAX_BOARD) {
        shake(btn);
        return;
      }
      state.selections.set(id, "board");
    } else if (current === "board") {
      // Switch to hand if there's room.
      if (countState("hand") >= MAX_HAND) {
        shake(btn);
        return;
      }
      state.selections.set(id, "hand");
    } else {
      // Third tap → deselect.
      state.selections.delete(id);
    }
    render();
    scheduleEquityUpdate();
  }

  function shake(btn) {
    btn.classList.remove("mini-card--shake");
    // Force reflow to restart animation.
    void btn.offsetWidth;
    btn.classList.add("mini-card--shake");
  }

  function clearAll() {
    state.selections.clear();
    render();
    scheduleEquityUpdate();
  }

  // ── Rendering ─────────────────────────────────────────────────

  function parseCardId(id) {
    return { rank: id[0], suitCode: id[1] };
  }

  function suitInfo(code) {
    return SUITS.find((s) => s.code === code);
  }

  function renderBigCard(id) {
    const { rank, suitCode } = parseCardId(id);
    const suit = suitInfo(suitCode);
    const target = state.selections.get(id);

    const div = document.createElement("div");
    div.className = "card-big " + (suit.color === "red" ? "card-big--red" : "card-big--black");
    if (target === "hand")  div.classList.add("card-big--hand");
    if (target === "board") div.classList.add("card-big--board");

    const r = document.createElement("span");
    r.className = "card-big__rank";
    r.textContent = displayRank(rank);
    const s = document.createElement("span");
    s.className = "card-big__suit";
    s.textContent = suit.glyph;

    div.appendChild(r);
    div.appendChild(s);
    return div;
  }

  function render() {
    // Update pick grid classes.
    document.querySelectorAll(".mini-card").forEach((btn) => {
      const id = btn.dataset.cardId;
      const target = state.selections.get(id);
      btn.classList.toggle("mini-card--hand",  target === "hand");
      btn.classList.toggle("mini-card--board", target === "board");
    });

    const handCards  = [];
    const boardCards = [];
    state.selections.forEach((v, k) => {
      if (v === "hand")  handCards.push(k);
      if (v === "board") boardCards.push(k);
    });

    el.handCount.textContent  = `(${handCards.length}/${MAX_HAND})`;
    el.boardCount.textContent = `(${boardCards.length}/${MAX_BOARD})`;

    el.handDisplay.innerHTML = "";
    if (handCards.length === 0) {
      const e = document.createElement("div");
      e.className = "selection-empty";
      e.textContent = "Tap a card to add it";
      el.handDisplay.appendChild(e);
    } else {
      handCards.forEach((id) => el.handDisplay.appendChild(renderBigCard(id)));
    }

    el.boardDisplay.innerHTML = "";
    if (boardCards.length === 0) {
      const e = document.createElement("div");
      e.className = "selection-empty";
      e.textContent = "No community cards yet";
      el.boardDisplay.appendChild(e);
    } else {
      boardCards.forEach((id) => el.boardDisplay.appendChild(renderBigCard(id)));
    }
  }

  // ── Equity API ────────────────────────────────────────────────

  let debounceTimer = null;

  function scheduleEquityUpdate() {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(updateEquity, DEBOUNCE_MS);
  }

  async function updateEquity() {
    const hand = [];
    const board = [];
    state.selections.forEach((v, k) => {
      if (v === "hand")  hand.push(k);
      if (v === "board") board.push(k);
    });

    if (hand.length < 2) {
      showPrompt("Select your 2 hole cards to begin");
      return;
    }

    // Cancel any in-flight request.
    if (state.inFlight) state.inFlight.abort();
    const controller = new AbortController();
    state.inFlight = controller;

    showSpinner(true);

    try {
      const res = await fetch("api/equity", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          hand,
          board,
          numPlayers: state.numPlayers,
        }),
        signal: controller.signal,
      });
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || res.statusText);
      }
      const data = await res.json();
      showResult(data);
    } catch (err) {
      if (err.name === "AbortError") return;
      console.error("equity request failed:", err);
      showPrompt("Error: " + err.message);
    } finally {
      if (state.inFlight === controller) state.inFlight = null;
      showSpinner(false);
    }
  }

  function showPrompt(msg) {
    el.resultPrompt.hidden = false;
    el.resultPrompt.textContent = msg;
    el.resultContent.hidden = true;
  }

  function showResult(data) {
    el.resultPrompt.hidden = true;
    el.resultContent.hidden = false;

    const winPct = (data.winProbability * 100).toFixed(1);
    const tiePct = (data.tieProbability * 100).toFixed(1);

    el.winPct.textContent  = winPct + "%";
    el.winValue.textContent = winPct + "%";
    el.tieValue.textContent = tiePct + "%";
    el.handDesc.textContent = data.handDescription || "—";

    const dashOffset = RING_CIRC * (1 - data.winProbability);
    el.ringFg.style.strokeDashoffset = dashOffset.toString();
  }

  function showSpinner(visible) {
    el.spinner.hidden = !visible;
  }

  // ── Events ────────────────────────────────────────────────────

  el.numPlayers.addEventListener("change", (e) => {
    state.numPlayers = parseInt(e.target.value, 10);
    scheduleEquityUpdate();
  });
  el.clearAll.addEventListener("click", clearAll);

  // ── Init ──────────────────────────────────────────────────────

  el.ringFg.style.strokeDasharray = RING_CIRC.toString();
  el.ringFg.style.strokeDashoffset = RING_CIRC.toString();

  buildPicker();
  render();
})();
