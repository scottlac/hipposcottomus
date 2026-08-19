package main

// vote.go — backend for the Ranked Choice Voting tool at /vote/.
//
// The first app on this site with user-writable persistent state, so the
// rules are stricter than the read-only dashboards:
//
//   - Every input is validated and capped (candidates, roster, label
//     lengths, body size) before it touches the store.
//   - All user-supplied strings are cleaned: valid UTF-8 only, control
//     characters stripped, length-limited. The frontend additionally
//     renders them exclusively through textContent.
//   - Poll creation is rate-limited per client IP and globally capped so
//     nobody can fill the 1Gi data volume.
//   - Admin actions authenticate with a bearer token whose SHA-256 (never
//     the token itself) is stored. Voters get a per-ballot claim token,
//     letting them edit their own ballot until the poll closes.
//   - Polls expire after 90 days; a background sweeper purges them.
//
// Deployment note: the store is a single in-memory map checkpointed to
// JSON on the PVC — correct because the site runs one replica with a
// Recreate strategy (see k8s/manifests.yaml).

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	voteBasePath = "/vote"
	voteFile     = "polls.json"

	votePollTTL       = 90 * 24 * time.Hour
	voteSweepInterval = 1 * time.Hour

	// Hard caps — enforced on creation, sized for game groups, generous
	// enough for a big table, small enough that the tally engine's
	// sensitivity re-runs stay trivially cheap.
	voteMaxPolls      = 2000
	voteMaxCandidates = 20
	voteMaxRoster     = 60
	voteMaxLabelLen   = 120
	voteMaxTitleLen   = 140
	voteMaxBody       = 64 << 10 // 64 KB request body cap

	// Poll creation rate limit per client IP: bucket of 5, one new
	// token every 10 minutes. A DnD group never notices; a script does.
	voteCreateBurst  = 5
	voteCreateRefill = 10 * time.Minute
)

// ── Data model ───────────────────────────────────────────────────

type VoteCandidate struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type VoteVoter struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type VoteBallot struct {
	VoterID string    `json:"voterId"`
	Ranking []string  `json:"ranking"`
	CastAt  time.Time `json:"castAt"`
	// ClaimHash is the SHA-256 of the voter's claim token. Persisted,
	// never sent to clients.
	ClaimHash string `json:"claimHash"`
}

type VotePoll struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Candidates   []VoteCandidate `json:"candidates"`
	Roster       []VoteVoter     `json:"roster"`
	SecretBallot bool            `json:"secretBallot"`
	LiveResults  bool            `json:"liveResults"`
	Ballots      []VoteBallot    `json:"ballots"`
	Closed       bool            `json:"closed"`
	CreatedAt    time.Time       `json:"createdAt"`
	ExpiresAt    time.Time       `json:"expiresAt"`
	// AdminHash is the SHA-256 of the admin token. Persisted, never
	// sent to clients.
	AdminHash string `json:"adminHash"`
}

func (p *VotePoll) ballotFor(voterID string) *VoteBallot {
	for i := range p.Ballots {
		if p.Ballots[i].VoterID == voterID {
			return &p.Ballots[i]
		}
	}
	return nil
}

func (p *VotePoll) voter(voterID string) *VoteVoter {
	for i := range p.Roster {
		if p.Roster[i].ID == voterID {
			return &p.Roster[i]
		}
	}
	return nil
}

func (p *VotePoll) candidateIDs() []string {
	ids := make([]string, len(p.Candidates))
	for i, c := range p.Candidates {
		ids[i] = c.ID
	}
	return ids
}

// ── Store ────────────────────────────────────────────────────────

type voteStore struct {
	mu    sync.RWMutex
	Polls map[string]*VotePoll `json:"polls"`
}

var polls = &voteStore{Polls: map[string]*VotePoll{}}

func votePath() string { return filepath.Join(getDataDir(), voteFile) }

func (s *voteStore) saveLocked() {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		log.Printf("[Vote] marshal failed: %v", err)
		return
	}
	path := votePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("[Vote] mkdir failed: %v", err)
		return
	}
	// Write-then-rename so a crash mid-write can't corrupt the only copy.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("[Vote] save failed: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[Vote] rename failed: %v", err)
	}
}

func (s *voteStore) load() {
	data, err := os.ReadFile(votePath())
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("[Vote] no persisted polls found, starting fresh")
		} else {
			log.Printf("[Vote] warning: failed to load polls: %v", err)
		}
		return
	}
	var loaded voteStore
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("[Vote] warning: failed to parse polls file: %v", err)
		return
	}
	s.mu.Lock()
	if loaded.Polls != nil {
		s.Polls = loaded.Polls
	}
	n := len(s.Polls)
	s.mu.Unlock()
	log.Printf("[Vote] loaded %d polls from disk", n)
}

// sweep deletes expired polls and checkpoints if anything changed.
func (s *voteStore) sweep() {
	now := time.Now()
	s.mu.Lock()
	removed := 0
	for id, p := range s.Polls {
		if now.After(p.ExpiresAt) {
			delete(s.Polls, id)
			removed++
		}
	}
	if removed > 0 {
		s.saveLocked()
	}
	s.mu.Unlock()
	if removed > 0 {
		log.Printf("[Vote] swept %d expired polls", removed)
	}
}

// ── Tokens & IDs ─────────────────────────────────────────────────

var voteIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// randomToken returns a URL-safe random string of n bytes of entropy,
// lowercase base32 (no padding, no ambiguous casing in chat apps).
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is a broken host; better to crash loudly
		// than mint predictable poll IDs or admin tokens.
		log.Fatalf("[Vote] crypto/rand failed: %v", err)
	}
	return strings.ToLower(voteIDEncoding.EncodeToString(b))
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func tokenMatches(tok, wantHash string) bool {
	if tok == "" || wantHash == "" {
		return false
	}
	got := hashToken(tok)
	return subtle.ConstantTimeCompare([]byte(got), []byte(wantHash)) == 1
}

// ── Input cleaning ───────────────────────────────────────────────

// cleanLabel validates and normalizes one user-supplied string: must be
// valid UTF-8, control characters stripped, whitespace collapsed, and
// non-empty within maxLen. Second return is false on rejection.
func cleanLabel(s string, maxLen int) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		// encoding/json coerces invalid UTF-8 to U+FFFD before we ever
		// see it, so treat replacement chars as junk too.
		if unicode.IsControl(r) || r == utf8.RuneError {
			continue
		}
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if out == "" || utf8.RuneCountInString(out) > maxLen {
		return "", false
	}
	return out, true
}

// ── Rate limiting ────────────────────────────────────────────────

type voteBucket struct {
	tokens float64
	last   time.Time
}

var (
	voteRLMu      sync.Mutex
	voteRLBuckets = map[string]*voteBucket{}
)

// voteAllowCreate implements a token bucket per client IP for poll
// creation. Stale buckets are pruned opportunistically.
func voteAllowCreate(r *http.Request) bool {
	key := "unknown"
	if ip := clientIP(r); ip != nil {
		key = ip.String()
	}
	now := time.Now()

	voteRLMu.Lock()
	defer voteRLMu.Unlock()

	if len(voteRLBuckets) > 10000 {
		for k, b := range voteRLBuckets {
			if now.Sub(b.last) > time.Hour {
				delete(voteRLBuckets, k)
			}
		}
	}

	b, ok := voteRLBuckets[key]
	if !ok {
		b = &voteBucket{tokens: voteCreateBurst, last: now}
		voteRLBuckets[key] = b
	}
	b.tokens += now.Sub(b.last).Minutes() / voteCreateRefill.Minutes()
	if b.tokens > voteCreateBurst {
		b.tokens = voteCreateBurst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ── API: create ──────────────────────────────────────────────────

type voteCreateRequest struct {
	Title        string   `json:"title"`
	Candidates   []string `json:"candidates"`
	Roster       []string `json:"roster"`
	SecretBallot bool     `json:"secretBallot"`
	LiveResults  bool     `json:"liveResults"`
}

type voteCreateResponse struct {
	PollID     string `json:"pollId"`
	AdminToken string `json:"adminToken"`
}

func voteHTTPError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decodeVoteBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, voteMaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		voteHTTPError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func handleVoteCreate(w http.ResponseWriter, r *http.Request) {
	if !voteAllowCreate(r) {
		voteHTTPError(w, http.StatusTooManyRequests, "too many polls created from this address — try again later")
		return
	}
	var req voteCreateRequest
	if !decodeVoteBody(w, r, &req) {
		return
	}

	title, ok := cleanLabel(req.Title, voteMaxTitleLen)
	if !ok {
		voteHTTPError(w, http.StatusBadRequest, "title is required (max 140 characters)")
		return
	}
	if len(req.Candidates) < 2 || len(req.Candidates) > voteMaxCandidates {
		voteHTTPError(w, http.StatusBadRequest, fmt.Sprintf("between 2 and %d options required", voteMaxCandidates))
		return
	}
	if len(req.Roster) < 1 || len(req.Roster) > voteMaxRoster {
		voteHTTPError(w, http.StatusBadRequest, fmt.Sprintf("between 1 and %d voters required", voteMaxRoster))
		return
	}

	poll := &VotePoll{
		Title:       title,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(votePollTTL),
		LiveResults: req.LiveResults,
	}
	poll.SecretBallot = req.SecretBallot

	seenLabels := map[string]bool{}
	for i, raw := range req.Candidates {
		label, ok := cleanLabel(raw, voteMaxLabelLen)
		if !ok {
			voteHTTPError(w, http.StatusBadRequest, "every option needs a name (max 120 characters)")
			return
		}
		if key := strings.ToLower(label); seenLabels[key] {
			voteHTTPError(w, http.StatusBadRequest, "duplicate option: "+label)
			return
		} else {
			seenLabels[key] = true
		}
		poll.Candidates = append(poll.Candidates, VoteCandidate{ID: fmt.Sprintf("c%02d", i+1), Label: label})
	}

	seenNames := map[string]bool{}
	for i, raw := range req.Roster {
		name, ok := cleanLabel(raw, voteMaxLabelLen)
		if !ok {
			voteHTTPError(w, http.StatusBadRequest, "every voter needs a name (max 120 characters)")
			return
		}
		if key := strings.ToLower(name); seenNames[key] {
			voteHTTPError(w, http.StatusBadRequest, "duplicate voter: "+name)
			return
		} else {
			seenNames[key] = true
		}
		poll.Roster = append(poll.Roster, VoteVoter{ID: fmt.Sprintf("v%02d", i+1), Name: name})
	}

	adminToken := randomToken(16)
	poll.AdminHash = hashToken(adminToken)

	polls.mu.Lock()
	if len(polls.Polls) >= voteMaxPolls {
		polls.mu.Unlock()
		voteHTTPError(w, http.StatusServiceUnavailable, "poll storage is full — expired polls are cleaned hourly, try again later")
		return
	}
	// 8-char IDs (40 bits): collisions vanishingly unlikely at this
	// scale, but re-roll on one anyway.
	var id string
	for {
		id = randomToken(5)
		if _, exists := polls.Polls[id]; !exists {
			break
		}
	}
	poll.ID = id
	polls.Polls[id] = poll
	polls.saveLocked()
	polls.mu.Unlock()

	log.Printf("[Vote] poll %s created (%d options, %d voters)", id, len(poll.Candidates), len(poll.Roster))
	writeJSON(w, voteCreateResponse{PollID: id, AdminToken: adminToken})
}

// ── API: poll status ─────────────────────────────────────────────

type voteRosterEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Voted bool   `json:"voted"`
}

type votePollResponse struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Candidates     []VoteCandidate   `json:"candidates"`
	Roster         []voteRosterEntry `json:"roster"`
	SecretBallot   bool              `json:"secretBallot"`
	LiveResults    bool              `json:"liveResults"`
	Closed         bool              `json:"closed"`
	VotedCount     int               `json:"votedCount"`
	CreatedAt      time.Time         `json:"createdAt"`
	ExpiresAt      time.Time         `json:"expiresAt"`
	IsAdmin        bool              `json:"isAdmin"`
	ResultsVisible bool              `json:"resultsVisible"`
}

// lookupPoll fetches a live poll or writes a 404. Callers hold no lock.
func lookupPoll(w http.ResponseWriter, id string) *VotePoll {
	polls.mu.RLock()
	p := polls.Polls[id]
	polls.mu.RUnlock()
	if p == nil || time.Now().After(p.ExpiresAt) {
		voteHTTPError(w, http.StatusNotFound, "poll not found — it may have expired (polls live 90 days)")
		return nil
	}
	return p
}

func isVoteAdmin(r *http.Request, p *VotePoll) bool {
	return tokenMatches(r.Header.Get("X-Admin-Token"), p.AdminHash)
}

func handleVotePollGet(w http.ResponseWriter, r *http.Request) {
	p := lookupPoll(w, r.PathValue("id"))
	if p == nil {
		return
	}
	isAdmin := isVoteAdmin(r, p)

	polls.mu.RLock()
	resp := votePollResponse{
		ID: p.ID, Title: p.Title, Candidates: p.Candidates,
		SecretBallot: p.SecretBallot, LiveResults: p.LiveResults,
		Closed: p.Closed, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt,
		IsAdmin:        isAdmin,
		ResultsVisible: p.Closed || p.LiveResults || isAdmin,
	}
	for _, v := range p.Roster {
		voted := p.ballotFor(v.ID) != nil
		if voted {
			resp.VotedCount++
		}
		resp.Roster = append(resp.Roster, voteRosterEntry{ID: v.ID, Name: v.Name, Voted: voted})
	}
	polls.mu.RUnlock()

	writeJSON(w, resp)
}

// ── API: cast / edit a ballot ────────────────────────────────────

type voteBallotRequest struct {
	VoterID    string   `json:"voterId"`
	Ranking    []string `json:"ranking"`
	ClaimToken string   `json:"claimToken"`
}

func handleVoteBallot(w http.ResponseWriter, r *http.Request) {
	p := lookupPoll(w, r.PathValue("id"))
	if p == nil {
		return
	}
	var req voteBallotRequest
	if !decodeVoteBody(w, r, &req) {
		return
	}

	polls.mu.Lock()
	defer polls.mu.Unlock()

	if p.Closed {
		voteHTTPError(w, http.StatusConflict, "this poll is closed")
		return
	}
	if p.voter(req.VoterID) == nil {
		voteHTTPError(w, http.StatusBadRequest, "unknown voter")
		return
	}
	if err := validateRanking(p, req.Ranking); err != nil {
		voteHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing := p.ballotFor(req.VoterID)
	if existing != nil {
		// Editing an existing ballot requires the claim token minted
		// when it was cast. Prevents drive-by overwrites of someone
		// else's vote; the admin can clear a ballot if a name was
		// claimed by the wrong person.
		if !tokenMatches(req.ClaimToken, existing.ClaimHash) {
			voteHTTPError(w, http.StatusForbidden, "this name has already voted — if that's you, use the device you voted from, or ask the poll creator to clear the ballot")
			return
		}
		existing.Ranking = req.Ranking
		existing.CastAt = time.Now()
		polls.saveLocked()
		writeJSON(w, map[string]string{"claimToken": req.ClaimToken})
		return
	}

	claim := randomToken(16)
	p.Ballots = append(p.Ballots, VoteBallot{
		VoterID:   req.VoterID,
		Ranking:   req.Ranking,
		CastAt:    time.Now(),
		ClaimHash: hashToken(claim),
	})
	polls.saveLocked()
	writeJSON(w, map[string]string{"claimToken": claim})
}

func validateRanking(p *VotePoll, ranking []string) error {
	if len(ranking) == 0 {
		return errors.New("rank at least one option")
	}
	if len(ranking) > len(p.Candidates) {
		return errors.New("ranking has more entries than there are options")
	}
	known := map[string]bool{}
	for _, c := range p.Candidates {
		known[c.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range ranking {
		if !known[id] {
			return errors.New("ranking references an unknown option")
		}
		if seen[id] {
			return errors.New("ranking lists the same option twice")
		}
		seen[id] = true
	}
	return nil
}

// ── API: admin actions ───────────────────────────────────────────

func requireVoteAdmin(w http.ResponseWriter, r *http.Request) *VotePoll {
	p := lookupPoll(w, r.PathValue("id"))
	if p == nil {
		return nil
	}
	if !isVoteAdmin(r, p) {
		voteHTTPError(w, http.StatusForbidden, "admin token required")
		return nil
	}
	return p
}

func handleVoteClose(w http.ResponseWriter, r *http.Request) {
	setVoteClosed(w, r, true)
}

func handleVoteReopen(w http.ResponseWriter, r *http.Request) {
	setVoteClosed(w, r, false)
}

func setVoteClosed(w http.ResponseWriter, r *http.Request, closed bool) {
	p := requireVoteAdmin(w, r)
	if p == nil {
		return
	}
	polls.mu.Lock()
	p.Closed = closed
	polls.saveLocked()
	polls.mu.Unlock()
	writeJSON(w, map[string]bool{"closed": closed})
}

type voteClearRequest struct {
	VoterID string `json:"voterId"`
}

// handleVoteClearBallot lets the admin delete one voter's ballot — the
// remedy when someone picks the wrong name off the roster.
func handleVoteClearBallot(w http.ResponseWriter, r *http.Request) {
	p := requireVoteAdmin(w, r)
	if p == nil {
		return
	}
	var req voteClearRequest
	if !decodeVoteBody(w, r, &req) {
		return
	}
	polls.mu.Lock()
	defer polls.mu.Unlock()
	for i := range p.Ballots {
		if p.Ballots[i].VoterID == req.VoterID {
			p.Ballots = append(p.Ballots[:i], p.Ballots[i+1:]...)
			polls.saveLocked()
			writeJSON(w, map[string]bool{"cleared": true})
			return
		}
	}
	voteHTTPError(w, http.StatusNotFound, "no ballot for that voter")
}

// ── API: results ─────────────────────────────────────────────────

type voteResultBallot struct {
	// Label is the voter's name, or "Ballot N" on secret-ballot polls.
	Label   string   `json:"label"`
	Ranking []string `json:"ranking"`
}

type voteResultsResponse struct {
	Title        string             `json:"title"`
	Candidates   []VoteCandidate    `json:"candidates"`
	SecretBallot bool               `json:"secretBallot"`
	Closed       bool               `json:"closed"`
	ExpiresAt    time.Time          `json:"expiresAt"`
	Ballots      []voteResultBallot `json:"ballots"`
	NotVoted     []string           `json:"notVoted"` // roster names still outstanding
	Result       TallyResult        `json:"result"`
}

func handleVoteResults(w http.ResponseWriter, r *http.Request) {
	p := lookupPoll(w, r.PathValue("id"))
	if p == nil {
		return
	}
	isAdmin := isVoteAdmin(r, p)

	polls.mu.RLock()
	defer polls.mu.RUnlock()

	if !p.Closed && !p.LiveResults && !isAdmin {
		voteHTTPError(w, http.StatusForbidden, "results are hidden until the poll is closed")
		return
	}

	resp := voteResultsResponse{
		Title: p.Title, Candidates: p.Candidates,
		SecretBallot: p.SecretBallot, Closed: p.Closed, ExpiresAt: p.ExpiresAt,
	}

	rankings := make([][]string, 0, len(p.Ballots))
	for _, b := range p.Ballots {
		rankings = append(rankings, b.Ranking)
	}
	resp.Result = RunTally(TallyInput{CandidateIDs: p.candidateIDs(), Ballots: rankings})

	if p.SecretBallot {
		// Detach ballots from voters. Sort by ranking content so the
		// order carries no trace of who voted when.
		sorted := make([][]string, len(rankings))
		copy(sorted, rankings)
		sort.Slice(sorted, func(i, j int) bool {
			return strings.Join(sorted[i], ",") < strings.Join(sorted[j], ",")
		})
		for i, rk := range sorted {
			resp.Ballots = append(resp.Ballots, voteResultBallot{Label: fmt.Sprintf("Ballot %d", i+1), Ranking: rk})
		}
	} else {
		for _, b := range p.Ballots {
			name := b.VoterID
			if v := p.voter(b.VoterID); v != nil {
				name = v.Name
			}
			resp.Ballots = append(resp.Ballots, voteResultBallot{Label: name, Ranking: b.Ranking})
		}
	}

	for _, v := range p.Roster {
		if p.ballotFor(v.ID) == nil {
			resp.NotVoted = append(resp.NotVoted, v.Name)
		}
	}

	writeJSON(w, resp)
}

// ── Registration ─────────────────────────────────────────────────

// InitVote registers the ranked choice voting tool: API routes, the SPA
// page (with per-poll OG meta injected, same pattern as poker), and its
// embedded static assets.
func InitVote(mux *http.ServeMux) {
	polls.load()
	polls.sweep()
	go func() {
		ticker := time.NewTicker(voteSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			polls.sweep()
		}
	}()

	mux.HandleFunc("POST "+voteBasePath+"/api/poll", handleVoteCreate)
	mux.HandleFunc("GET "+voteBasePath+"/api/poll/{id}", handleVotePollGet)
	mux.HandleFunc("POST "+voteBasePath+"/api/poll/{id}/ballot", handleVoteBallot)
	mux.HandleFunc("POST "+voteBasePath+"/api/poll/{id}/close", handleVoteClose)
	mux.HandleFunc("POST "+voteBasePath+"/api/poll/{id}/reopen", handleVoteReopen)
	mux.HandleFunc("POST "+voteBasePath+"/api/poll/{id}/clear-ballot", handleVoteClearBallot)
	mux.HandleFunc("GET "+voteBasePath+"/api/poll/{id}/results", handleVoteResults)
	mux.HandleFunc("GET "+voteBasePath+"/og.png", handleVoteOGImage)

	staticSub, err := fsSubVote()
	if err != nil {
		log.Fatalf("[Vote] failed to load embedded static files: %v", err)
	}
	loadVoteHTMLTemplate(staticSub)
	fileServer := http.FileServer(http.FS(staticSub))

	// One HTML page serves three client-side views. /vote/p/{id} and
	// /vote/r/{id} are real paths (clean share links), so they must
	// serve the page too; everything else under /vote/ is an asset.
	mux.HandleFunc(voteBasePath+"/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == voteBasePath+"/" || path == voteBasePath+"/index.html",
			strings.HasPrefix(path, voteBasePath+"/p/"),
			strings.HasPrefix(path, voteBasePath+"/r/"):
			serveVoteHTML(w, r)
		default:
			http.StripPrefix(voteBasePath, fileServer).ServeHTTP(w, r)
		}
	})

	log.Printf("[Vote] ranked choice voting registered at %s/", voteBasePath)
}
