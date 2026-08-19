/* Ranked Choice Voting — frontend.
 *
 * Three client-side views on one page:
 *   /vote/        create a poll
 *   /vote/p/{id}  cast / edit a ballot (plus admin controls)
 *   /vote/r/{id}  results
 *
 * Security rule for this file: every user-supplied string (poll titles,
 * option labels, voter names — including ones arriving via the API from
 * other people) is rendered ONLY through textContent. No innerHTML in
 * any render path.
 */
"use strict";

const API = "/vote/api";

/* Validated categorical palette (dark surface #0f172a) — assigned to
 * candidates in creation order, never cycled. Beyond 8 candidates the
 * bars go neutral and identity rides on the row labels alone. */
const PALETTE = ["#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"];
const NEUTRAL_BAR = "#64748b";

const TIE_RULE_TEXT = {
  prevRound: "had the fewest votes in the previous round",
  fewestRankings: "was ranked on the fewest ballots overall",
  listedLast: "was listed last when the poll was created",
};

/* ── Tiny DOM helpers (textContent only) ─────────────────────── */

function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (k === "class") el.className = v;
      else if (k === "text") el.textContent = v;
      else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
      else if (v !== false && v != null) el.setAttribute(k, v === true ? "" : v);
    }
  }
  for (const c of children) {
    if (c == null) continue;
    el.append(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return el;
}

const app = document.getElementById("app");
function show(...nodes) { app.replaceChildren(...nodes); }

/* ── Local tokens ────────────────────────────────────────────── */

const store = {
  adminKey: (id) => `rcv-admin-${id}`,
  claimKey: (id) => `rcv-claim-${id}`,
  getAdmin(id) { try { return localStorage.getItem(this.adminKey(id)) || ""; } catch { return ""; } },
  setAdmin(id, tok) { try { localStorage.setItem(this.adminKey(id), tok); } catch {} },
  getClaim(id) {
    try { return JSON.parse(localStorage.getItem(this.claimKey(id))) || null; } catch { return null; }
  },
  setClaim(id, voterId, token) {
    try { localStorage.setItem(this.claimKey(id), JSON.stringify({ voterId, token })); } catch {}
  },
};

/* ── API helpers ─────────────────────────────────────────────── */

async function api(method, path, body, adminToken) {
  const headers = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (adminToken) headers["X-Admin-Token"] = adminToken;
  const res = await fetch(API + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let data = null;
  try { data = await res.json(); } catch {}
  if (!res.ok) {
    const err = new Error((data && data.error) || `request failed (${res.status})`);
    err.status = res.status;
    throw err;
  }
  return data;
}

/* ── Router ──────────────────────────────────────────────────── */

function route() {
  const path = location.pathname;
  // Admin tokens arrive in the fragment (#a=...) so they never hit
  // server logs; stash and strip on arrival.
  const frag = new URLSearchParams(location.hash.replace(/^#/, ""));
  const m = path.match(/^\/vote\/([pr])\/([a-z2-7]{4,20})\/?$/);
  if (m && frag.get("a")) {
    store.setAdmin(m[2], frag.get("a"));
    history.replaceState(null, "", path);
  }
  if (!m) { renderCreate(); return; }
  if (m[1] === "p") renderBallot(m[2]);
  else renderResults(m[2]);
}

/* ══ CREATE VIEW ══════════════════════════════════════════════ */

function listEditor(placeholder, initial) {
  const wrap = h("div", { class: "list-editor" });
  function addRow(value) {
    const input = h("input", { type: "text", placeholder, maxlength: "120" });
    input.value = value || "";
    const row = h("div", { class: "list-row" },
      input,
      h("button", {
        type: "button", "aria-label": "Remove", text: "✕",
        onclick: () => { if (wrap.children.length > 1) row.remove(); else input.value = ""; },
      }),
    );
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        if (row === wrap.lastElementChild) addRow().querySelector("input").focus();
        else row.nextElementSibling?.querySelector("input")?.focus();
      }
    });
    wrap.append(row);
    return row;
  }
  for (const v of initial) addRow(v);
  return {
    el: wrap,
    addRow,
    values: () => [...wrap.querySelectorAll("input")].map((i) => i.value.trim()).filter(Boolean),
  };
}

function renderCreate() {
  document.title = "Ranked Choice Voting";
  const title = h("input", { type: "text", placeholder: "What are we deciding?", maxlength: "140" });
  const options = listEditor("Option (e.g. Just Ask Them)", ["", "", ""]);
  const players = listEditor("Player name", ["", "", "", ""]);
  const secret = h("input", { type: "checkbox" });
  const live = h("input", { type: "checkbox" });
  const errBox = h("p", { class: "error-text" });

  async function create() {
    errBox.textContent = "";
    try {
      const resp = await api("POST", "/poll", {
        title: title.value.trim(),
        candidates: options.values(),
        roster: players.values(),
        secretBallot: secret.checked,
        liveResults: live.checked,
      });
      store.setAdmin(resp.pollId, resp.adminToken);
      renderShare(resp.pollId, resp.adminToken, title.value.trim());
    } catch (e) {
      errBox.textContent = e.message;
    }
  }

  show(
    h("section", { class: "panel" },
      h("h2", { text: "Create a poll" }),
      h("label", { text: "Question" }), title,
      h("h3", { text: "Options" }),
      h("p", { class: "hint", text: "The choices everyone will rank. 2–20." }),
      options.el,
      h("button", { type: "button", class: "add-btn", text: "+ Add option", onclick: () => options.addRow().querySelector("input").focus() }),
      h("h3", { text: "Players" }),
      h("p", { class: "hint", text: "Who gets a ballot. Voters pick their own name from this list, so you always know who you're waiting on." }),
      players.el,
      h("button", { type: "button", class: "add-btn", text: "+ Add player", onclick: () => players.addRow().querySelector("input").focus() }),
      h("div", { class: "toggles" },
        h("label", {}, secret, " Secret ballots — results won't show who ranked what"),
        h("label", {}, live, " Live results — anyone can watch the tally before the poll closes"),
      ),
      h("button", { type: "button", text: "Create poll", onclick: create }),
      errBox,
    ),
  );
}

function copyBtn(getText) {
  const btn = h("button", { type: "button", class: "secondary-btn", text: "Copy" });
  btn.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(getText());
      btn.textContent = "Copied!";
      setTimeout(() => (btn.textContent = "Copy"), 1500);
    } catch {
      btn.textContent = "Select & copy";
    }
  });
  return btn;
}

function linkRow(url) {
  const input = h("input", { type: "text", readonly: true });
  input.value = url;
  input.addEventListener("focus", () => input.select());
  return h("div", { class: "share-link" }, input, copyBtn(() => url));
}

function renderShare(pollId, adminToken, pollTitle) {
  const base = location.origin;
  const voteURL = `${base}/vote/p/${pollId}`;
  const resultsURL = `${base}/vote/r/${pollId}`;
  const adminURL = `${base}/vote/p/${pollId}#a=${adminToken}`;

  show(
    h("section", { class: "panel" },
      h("h2", { text: "Poll created 🎉" }),
      h("p", { class: "muted", text: pollTitle }),
      h("h3", { text: "Share this link with your group" }),
      linkRow(voteURL),
      h("h3", { text: "Results" }),
      linkRow(resultsURL),
      h("h3", { text: "Admin link (keep private)" }),
      h("p", { class: "hint", text: "Closing the poll, reopening it, and clearing mistaken ballots happen from this link. It works in this browser already; save the link if you might need another device." }),
      linkRow(adminURL),
      h("div", { class: "admin-note", text: "Anyone with the admin link controls the poll — don't paste it in the group chat." }),
      h("div", { class: "btn-row" },
        h("a", { role: "button", href: `/vote/p/${pollId}`, text: "Open the poll →" }),
      ),
    ),
  );
}

/* ══ BALLOT VIEW ══════════════════════════════════════════════ */

function rosterChips(poll, myVoterId) {
  const wrap = h("div", { class: "roster" });
  for (const v of poll.roster) {
    const cls = ["roster-chip", v.voted ? "voted" : "", v.id === myVoterId ? "me" : ""].join(" ").trim();
    wrap.append(h("span", { class: cls }, v.voted ? "✓ " : "○ ", v.name));
  }
  return wrap;
}

async function renderBallot(pollId) {
  let poll;
  try {
    poll = await api("GET", `/poll/${pollId}`, undefined, store.getAdmin(pollId));
  } catch (e) {
    renderGone(e);
    return;
  }
  document.title = `Vote: ${poll.title}`;

  const claim = store.getClaim(pollId);
  const isAdmin = poll.isAdmin;

  const header = h("section", { class: "panel" },
    h("h2", { text: poll.title }),
    h("p", { class: "muted" }, `${poll.votedCount} of ${poll.roster.length} have voted`,
      poll.closed ? " · poll closed" : ""),
    rosterChips(poll, claim?.voterId),
  );

  const sections = [header];

  if (poll.closed) {
    sections.push(h("section", { class: "panel center" },
      h("p", { text: "This poll is closed." }),
      h("div", { class: "btn-row", style: "justify-content:center" },
        h("a", { role: "button", href: `/vote/r/${pollId}`, text: "See the results →" })),
    ));
  } else {
    sections.push(ballotPanel(pollId, poll, claim));
  }

  if (poll.resultsVisible && !poll.closed) {
    sections.push(h("p", { class: "center muted" },
      h("a", { href: `/vote/r/${pollId}`, text: "Peek at the live results" })));
  }

  if (isAdmin) sections.push(adminPanel(pollId, poll));

  show(...sections);
}

function ballotPanel(pollId, poll, claim) {
  const panel = h("section", { class: "panel" });
  const errBox = h("p", { class: "error-text" });

  // Step 1: who are you?
  const mine = claim && poll.roster.some((v) => v.id === claim.voterId);
  let selectedVoter = mine ? claim.voterId : null;

  const namePrompt = h("h3", { text: "Who are you?" });
  const nameGrid = h("div", { class: "name-grid" });
  const rankArea = h("div");

  function pickName(v, btn) {
    selectedVoter = v.id;
    [...nameGrid.querySelectorAll("button")].forEach((b) => b.setAttribute("aria-pressed", "false"));
    btn.setAttribute("aria-pressed", "true");
    renderRanker();
  }

  for (const v of poll.roster) {
    const isMine = claim?.voterId === v.id;
    const btn = h("button", {
      type: "button", class: "name-pick",
      "aria-pressed": claim?.voterId === v.id ? "true" : "false",
      text: v.voted ? `✓ ${v.name}` : v.name,
    });
    btn.addEventListener("click", () => pickName(v, btn));
    // A name that has voted is only pickable by whoever holds its claim
    // token (they can edit). Others get told to see the admin.
    if (v.voted && !isMine) {
      btn.disabled = true;
      btn.title = "Already voted. Wrong person claimed your name? Ask the poll creator to clear it.";
    }
    nameGrid.append(btn);
  }

  function renderRanker() {
    const editing = poll.roster.find((v) => v.id === selectedVoter)?.voted;
    let ranked = [];
    let pool = poll.candidates.map((c) => c.id);

    const rankList = h("div", { class: "rank-list" });
    const poolWrap = h("div", { class: "pool" });
    const label = (id) => poll.candidates.find((c) => c.id === id)?.label || id;

    function redraw() {
      rankList.replaceChildren(...ranked.map((id, i) =>
        h("div", { class: "rank-item" },
          h("span", { class: "rank-pos", text: String(i + 1) }),
          h("span", { class: "rank-label", text: label(id) }),
          h("button", {
            type: "button", class: "ctl", "aria-label": "Move up", text: "↑", disabled: i === 0,
            onclick: () => { [ranked[i - 1], ranked[i]] = [ranked[i], ranked[i - 1]]; redraw(); },
          }),
          h("button", {
            type: "button", class: "ctl", "aria-label": "Move down", text: "↓", disabled: i === ranked.length - 1,
            onclick: () => { [ranked[i + 1], ranked[i]] = [ranked[i], ranked[i + 1]]; redraw(); },
          }),
          h("button", {
            type: "button", class: "ctl", "aria-label": "Remove", text: "✕",
            onclick: () => { pool.push(id); ranked = ranked.filter((x) => x !== id); redraw(); },
          }),
        )));
      poolWrap.replaceChildren(...pool.map((id) =>
        h("button", {
          type: "button", text: label(id),
          onclick: () => { ranked.push(id); pool = pool.filter((x) => x !== id); redraw(); },
        })));
    }
    redraw();

    async function submit() {
      errBox.textContent = "";
      if (ranked.length === 0) { errBox.textContent = "Rank at least one option."; return; }
      try {
        const body = { voterId: selectedVoter, ranking: ranked };
        if (claim?.voterId === selectedVoter) body.claimToken = claim.token;
        const resp = await api("POST", `/poll/${pollId}/ballot`, body);
        store.setClaim(pollId, selectedVoter, resp.claimToken);
        renderBallot(pollId); // refresh: shows ✓ and progress
      } catch (e) {
        errBox.textContent = e.message;
      }
    }

    rankArea.replaceChildren(
      h("h3", { text: editing ? "Edit your ranking" : "Rank the options" }),
      h("p", { class: "hint", text: "Tap options in order of preference — #1 is your favorite. You don't have to rank everything: unranked options never get your vote, and if all your ranked picks are eliminated your ballot stops counting (it “exhausts”)." }),
      rankList,
      h("p", { class: "hint", text: "Still on the bench:" }),
      poolWrap,
      h("div", { class: "btn-row" },
        h("button", { type: "button", text: editing ? "Update my ballot" : "Cast my ballot", onclick: submit }),
      ),
      errBox,
    );
  }

  panel.append(namePrompt, nameGrid, rankArea);
  if (selectedVoter) renderRanker();
  return panel;
}

function adminPanel(pollId, poll) {
  const token = store.getAdmin(pollId);
  const errBox = h("p", { class: "error-text" });

  async function act(path, body) {
    errBox.textContent = "";
    try {
      await api("POST", `/poll/${pollId}/${path}`, body ?? {}, token);
      renderBallot(pollId);
    } catch (e) {
      errBox.textContent = e.message;
    }
  }

  const clearRows = poll.roster.filter((v) => v.voted).map((v) =>
    h("div", { class: "btn-row" },
      h("button", {
        type: "button", class: "danger", text: `Clear ${v.name}'s ballot`,
        onclick: () => { if (confirm(`Delete ${v.name}'s ballot? They'll have to vote again.`)) act("clear-ballot", { voterId: v.id }); },
      })));

  return h("section", { class: "panel" },
    h("h2", { text: "🔑 Admin" }),
    h("p", { class: "hint", text: poll.closed ? "The poll is closed — results are visible to everyone with the link." : "Close the poll to lock ballots and reveal the results." }),
    h("div", { class: "btn-row" },
      poll.closed
        ? h("button", { type: "button", class: "secondary-btn", text: "Reopen voting", onclick: () => act("reopen") })
        : h("button", { type: "button", text: "Close poll & reveal results", onclick: () => { if (confirm("Close the poll? Ballots lock and results become visible.")) act("close"); } }),
      h("a", { role: "button", class: "secondary", href: `/vote/r/${pollId}`, text: "View results" }),
    ),
    clearRows.length ? h("h3", { text: "Fix a mistaken ballot" }) : null,
    ...clearRows,
    errBox,
  );
}

/* ══ RESULTS VIEW ═════════════════════════════════════════════ */

async function renderResults(pollId) {
  let data;
  try {
    data = await api("GET", `/poll/${pollId}/results`, undefined, store.getAdmin(pollId));
  } catch (e) {
    if (e.status === 403) { renderHiddenResults(pollId); return; }
    renderGone(e);
    return;
  }
  document.title = `Results: ${data.title}`;

  const cands = data.candidates;
  const label = (id) => cands.find((c) => c.id === id)?.label || (id ? id : "—");
  const colorOf = (id) => {
    if (cands.length > PALETTE.length) return NEUTRAL_BAR;
    const i = cands.findIndex((c) => c.id === id);
    return i >= 0 ? PALETTE[i] : NEUTRAL_BAR;
  };
  const r = data.result;

  const sections = [];

  // Winner card
  if (r.winnerId) {
    sections.push(h("section", { class: "panel winner-card" },
      h("div", { class: "crown", text: "👑" }),
      h("div", { class: "label", text: "Winner · instant runoff" }),
      h("div", { class: "name", text: label(r.winnerId) }),
      h("div", { class: "sub", text: `${data.title} · ${r.totalBallots} ballot${r.totalBallots === 1 ? "" : "s"}${data.closed ? "" : " · still open"}` }),
    ));
  } else {
    sections.push(h("section", { class: "panel winner-card" },
      h("div", { class: "label", text: "No winner yet" }),
      h("div", { class: "sub", text: r.totalBallots === 0 ? "No ballots have been cast." : "Every ballot exhausted before a winner emerged." }),
    ));
  }

  // Honesty banners
  if (r.tieBreakSensitive) {
    sections.push(h("div", { class: "alert warn" },
      h("strong", { text: "⚠ This result hinged on a tie-break" }),
      `A different — but equally valid — tie-break choice would have elected ${r.alternateWinners.map(label).join(" or ")}. ` +
      "The tie-break rules are deterministic and listed under each round below, but know that the outcome was this close.",
    ));
  }
  if (r.winnerId && r.condorcet.winnerId && r.condorcet.winnerId !== r.winnerId) {
    sections.push(h("div", { class: "alert warn" },
      h("strong", { text: "⚠ Head-to-head disagrees with the runoff" }),
      `${label(r.condorcet.winnerId)} beats every other option in one-on-one matchups but was eliminated in the runoff. ` +
      "Instant runoff only looks at first choices each round, so a broadly liked compromise can fall early. The pairwise table below shows the matchups.",
    ));
  } else if (r.winnerId && r.condorcet.winnerId === r.winnerId) {
    sections.push(h("div", { class: "alert ok" },
      h("strong", { text: "✓ Solid win" }),
      `${label(r.winnerId)} also beats every other option head-to-head — the runoff and the pairwise count agree.`,
    ));
  } else if (r.winnerId && r.condorcet.hasCycle) {
    sections.push(h("div", { class: "alert info" },
      h("strong", { text: "⟳ No head-to-head champion exists" }),
      "The group's pairwise preferences form a cycle (think rock-paper-scissors), so no option beats all the others one-on-one. The runoff winner is as fair a pick as any.",
    ));
  }

  if (data.notVoted?.length && !data.closed) {
    sections.push(h("div", { class: "alert info" },
      h("strong", { text: "Still waiting on" }), data.notVoted.join(", ")));
  }

  // Round-by-round chart
  const roundsPanel = h("section", { class: "panel" }, h("h2", { text: "How the count went" }));
  for (const round of r.rounds) roundsPanel.append(renderRound(round, r, cands, label, colorOf));
  sections.push(roundsPanel);

  // Ballots
  if (data.ballots?.length) {
    const tbl = h("table", { class: "mini" },
      h("thead", {}, h("tr", {}, h("th", { text: data.secretBallot ? "Ballot" : "Voter" }), h("th", { text: "Ranking (best first)" }))),
      h("tbody", {}, ...data.ballots.map((b) =>
        h("tr", {}, h("td", { text: b.label }),
          h("td", {}, h("span", { class: "rank-seq" }, ...b.ranking.flatMap((id, i) => {
            const parts = [];
            if (i > 0) parts.push("  ›  ");
            parts.push(h("b", { text: label(id) }));
            return parts;
          }))),
        ))));
    sections.push(h("details", {},
      h("summary", { text: data.secretBallot ? "The ballots (anonymous)" : "The ballots" }),
      h("div", { class: "table-scroll" }, tbl)));
  }

  // Pairwise matrix
  if (cands.length > 1 && r.totalBallots > 0) {
    const m = r.condorcet.matrix;
    const tbl = h("table", { class: "mini" },
      h("thead", {}, h("tr", {}, h("th", { text: "prefers ↓ over →" }), ...cands.map((c) => h("th", { text: c.label })))),
      h("tbody", {}, ...cands.map((c, i) =>
        h("tr", {}, h("th", { text: c.label }), ...cands.map((d, j) => {
          if (i === j) return h("td", { text: "—" });
          const cls = m[i][j] > m[j][i] ? "win-cell" : m[i][j] < m[j][i] ? "lose-cell" : "";
          return h("td", { class: cls, text: String(m[i][j]) });
        })))));
    sections.push(h("details", {},
      h("summary", { text: "Head-to-head matchups (pairwise table)" }),
      h("p", { class: "hint", text: "Each cell counts the ballots preferring the row option over the column option. Green = wins that matchup." }),
      h("div", { class: "table-scroll" }, tbl)));
  }

  sections.push(h("p", { class: "center muted" },
    `This poll expires ${new Date(data.expiresAt).toLocaleDateString()} · `,
    h("a", { href: `/vote/p/${pollId}`, text: "back to the poll" })));

  show(...sections);

  // Live polls keep the tally fresh.
  if (!data.closed) scheduleRefresh(() => renderResults(pollId));
}

function renderRound(round, result, cands, label, colorOf) {
  const wrap = h("div", { class: "round" });
  wrap.append(h("div", { class: "round-head" },
    h("h3", { text: `Round ${round.number}` }),
    h("span", { class: "meta", text: `${round.continuing} counting · needs ${round.majority} to win` }),
  ));

  // Bars, in creation order, standing candidates only + exhausted bucket.
  const scale = Math.max(round.continuing, 1);
  const rows = h("div", { class: "bar-rows" });
  const majorityPct = Math.min((round.majority / scale) * 100, 100);

  for (const c of cands) {
    if (!(c.id in round.counts)) continue; // already eliminated in an earlier round
    const n = round.counts[c.id];
    const eliminated = round.eliminated?.includes(c.id);
    const pct = (n / scale) * 100;
    const isWinner = round.winnerId === c.id;
    rows.append(h("div", { class: `bar-row${eliminated ? " eliminated" : ""}` },
      h("span", { class: "bar-name", text: (isWinner ? "👑 " : "") + c.label, title: c.label }),
      h("div", { class: "bar-track" },
        h("div", {
          class: `bar-fill${n === 0 ? " zero" : ""}`,
          style: `width:calc(${pct.toFixed(1)}% - 4px);background:${colorOf(c.id)}`,
        }),
        h("div", { class: "majority-line", style: `left:${majorityPct.toFixed(1)}%` }),
        h("span", { class: "bar-count", text: String(n) }),
      ),
    ));
  }
  if (round.exhausted > 0) {
    const pct = (round.exhausted / scale) * 100;
    rows.append(h("div", { class: "bar-row exhausted-row" },
      h("span", { class: "bar-name", text: "exhausted", title: "ballots whose ranked options were all eliminated" }),
      h("div", { class: "bar-track" },
        h("div", { class: "bar-fill exhausted-fill", style: `width:calc(${pct.toFixed(1)}% - 4px)` }),
        h("span", { class: "bar-count", text: String(round.exhausted) }),
      ),
    ));
  }
  wrap.append(rows, h("div", { class: "majority-tag", text: `┆ majority line: ${round.majority} of ${round.continuing} still-counting ballots` }));

  // Plain-English narration.
  const note = h("p", { class: "round-note" });
  if (round.winnerId) {
    note.append(h("span", { class: "win", text: `${label(round.winnerId)} wins` }),
      ` with ${round.counts[round.winnerId]} of ${round.continuing} continuing ballots.`);
  } else if (round.tieBreak) {
    const t = round.tieBreak;
    note.append(
      `${t.tiedIds.map(label).join(", ")} tied for fewest votes. `,
      `${label(t.eliminatedId)} ${TIE_RULE_TEXT[t.rule] || "lost the tie-break"}, so it was eliminated. `,
      "Its ballots move to each voter's next choice.");
  } else if (round.eliminated?.length) {
    const zeros = round.eliminated.filter((id) => round.counts[id] === 0);
    if (zeros.length === round.eliminated.length) {
      note.append(`${round.eliminated.map(label).join(", ")} had no first-choice votes and ${round.eliminated.length === 1 ? "was" : "were"} eliminated.`);
    } else {
      note.append(`${round.eliminated.map(label).join(", ")} had the fewest votes and ${round.eliminated.length === 1 ? "was" : "were"} eliminated. Its ballots move to each voter's next choice.`);
    }
  }
  if (round.number === 1 && result.rounds.length > 1) {
    note.append(" (A candidate needs a majority of still-counting ballots to win — nobody had it yet.)");
  }
  wrap.append(note);
  return wrap;
}

function renderHiddenResults(pollId) {
  api("GET", `/poll/${pollId}`).then((poll) => {
    document.title = `Results: ${poll.title}`;
    show(
      h("section", { class: "panel center" },
        h("h2", { text: poll.title }),
        h("p", { text: "🙈 Results are hidden until the poll closes." }),
        h("p", { class: "muted", text: `${poll.votedCount} of ${poll.roster.length} have voted so far.` }),
        rosterChips(poll, store.getClaim(pollId)?.voterId),
        h("div", { class: "btn-row", style: "justify-content:center" },
          h("a", { role: "button", href: `/vote/p/${pollId}`, text: "Go vote →" })),
      ),
    );
    scheduleRefresh(() => renderResults(pollId));
  }).catch(renderGone);
}

function renderGone(e) {
  show(h("section", { class: "panel center" },
    h("h2", { text: "Poll not found" }),
    h("p", { class: "muted", text: e?.message || "It may have expired — polls live for 90 days." }),
    h("div", { class: "btn-row", style: "justify-content:center" },
      h("a", { role: "button", href: "/vote/", text: "Create a new poll" })),
  ));
}

/* Refresh at a gentle cadence while a poll is open; stop when the tab
 * is hidden so phones at the table don't burn battery. */
let refreshTimer = null;
function scheduleRefresh(fn) {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    if (document.visibilityState === "visible") fn();
    else scheduleRefresh(fn);
  }, 10000);
}

route();
