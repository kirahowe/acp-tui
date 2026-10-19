package core

import (
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The workspace pane shows work in progress that relay put there: what a
// tool showed the operator (a display), the calls waiting for approval
// (proposals) and what the latest reply cites. Everything in it comes from
// data relay sent. Model text is only searched for reference strings.

// ItemKind is what an item in the pane is.
type ItemKind int

const (
	ShownItem ItemKind = iota
	ProposalItem
)

// Proposal statuses.
const (
	StatusWaitingProposal = "waiting"
	StatusAllowed         = "allowed"
	StatusRejected        = "rejected"
	StatusCancelledAsk    = "cancelled"
)

// Source is a document a shown item draws on.
type Source struct{ Title, Ref string }

// Item is one thing in the pane.
type Item struct {
	Kind    ItemKind
	Ref     string // shown:2026-10-18/toolu_01abc, when relay knows it
	Path    string // the doc's path in relay's data, when relay says
	CallID  string
	At      string
	Title   string
	Text    string // a shown item's text: what trusting it vouches for
	Sources []Source
	Labels  Labels
	// Version and SHA256 pin the text that was listed. Version is a JSON
	// number kept as written. CanTrust and TrustCommand come from relay.
	Version      string
	SHA256       string
	CanTrust     bool
	TrustCommand string
	// A proposal's tool, arguments (JSON), relay's preview and status,
	// and the arguments the operator set (Permission.FixedArgs).
	Tool      string
	Input     string
	Preview   string
	Status    string
	FixedArgs []string
	// AskID ties a proposal the client saw itself to its request.
	AskID uint64
}

// Key names an item so a selection can follow it when the list changes.
func (it Item) Key() string {
	if it.Kind == ProposalItem {
		if it.CallID == "" {
			return "p|ask|" + strconv.FormatUint(it.AskID, 10)
		}
		return "p|" + it.CallID
	}
	switch {
	case it.Ref != "":
		return "s|" + it.Ref
	case it.Path != "":
		return "s|" + it.Path
	}
	return "s|" + it.CallID + "|" + it.Title
}

// Display is what a tool call carried for the operator in
// _meta["relay/display"].
type Display struct {
	Title, Body string
	Labels      Labels
	Sources     []Source
	Ref         string
}

// Citation is a reference string from the latest reply and what relay
// answered when asked about it. An empty Status means unchecked: the
// answer is pending, or relay cannot be asked.
type Citation struct {
	Ref    string // [[kind:id]] as written
	Kind   string
	Status string // "", "ok", "missing" or "external"
	Title  string
	Text   string
	Target string
	Labels Labels
}

// Citation statuses.
const (
	CiteOK       = "ok"
	CiteMissing  = "missing"
	CiteExternal = "external"
)

// TrustPrompt is a trust the operator is being asked to confirm. It holds
// the item as it was listed, so what is confirmed is what was shown.
type TrustPrompt struct {
	Item  Item
	Since time.Time
	Sent  bool
}

// Pane is the workspace pane's state.
type Pane struct {
	// Relay says relay answered _relay/items: Server is then the whole
	// list. Unavailable says it did not, so the pane falls back to Local,
	// which holds what the client saw itself.
	Relay       bool
	Unavailable bool
	Server      []Item
	Local       []Item // oldest first
	Cites       []Citation
	// Domains is the hosts that appeared in an item the operator approved
	// or trusted in this run, sorted. It only grows.
	Domains []string

	Focus  bool // keys go to the pane; on a narrow screen it is on screen
	Detail bool // the selected item fills the pane
	Sel    int
	SelKey string
	Scroll int // first line shown of the detail view
	Trust  *TrustPrompt
	// Message is the outcome of the last action, and Command the
	// trustCommand to show with it.
	Message string
	Command string
	// Failure is an error relay gave, shown escaped.
	Failure string
}

// PaneItems is the items the pane lists, newest first.
func PaneItems(s State) []Item {
	p := s.Pane
	if p.Relay {
		return p.Server
	}
	out := make([]Item, len(p.Local))
	for i, it := range p.Local {
		out[len(out)-1-i] = it
	}
	return out
}

// SelectedIndex is the index in PaneItems of the selected item, or -1.
func SelectedIndex(s State) int {
	items := PaneItems(s)
	if len(items) == 0 {
		return -1
	}
	if k := s.Pane.SelKey; k != "" {
		for i, it := range items {
			if it.Key() == k {
				return i
			}
		}
	}
	return min(max(s.Pane.Sel, 0), len(items)-1)
}

// Events about the pane.
type (
	// ItemsLoaded is relay's answer to _relay/items. Unavailable says the
	// agent does not have the method; Failed, that the call failed some
	// other way and the list should stay as it is.
	ItemsLoaded struct {
		At                  time.Time
		Items               []Item
		Unavailable, Failed bool
	}
	// CitationResolved is relay's answer to _relay/resolve for Cite.Ref.
	// Failed leaves the citation unchecked.
	CitationResolved struct {
		At     time.Time
		Cite   Citation
		Failed bool
	}
	// TrustDone is the outcome of _relay/trust for the doc at Path. Err
	// is empty when it worked.
	TrustDone struct {
		At     time.Time
		Path   string
		Labels Labels
		Err    string
	}
	// PaneKey is a key press for the pane. Max is, for scroll keys, the
	// furthest the detail view can scroll, which the shell takes from the
	// renderer.
	PaneKey struct {
		At     time.Time
		Action PaneAction
		Max    int
	}
)

func (e ItemsLoaded) at() time.Time      { return e.At }
func (e CitationResolved) at() time.Time { return e.At }
func (e TrustDone) at() time.Time        { return e.At }
func (e PaneKey) at() time.Time          { return e.At }

// PaneAction is what a key does in the pane.
type PaneAction int

const (
	PaneOther  PaneAction = iota // any key without a meaning cancels a trust prompt
	PaneToggle                   // the pane key: focus the pane, or leave it
	PaneUp
	PaneDown
	PagePrev
	PageNext
	PaneEnter
	PaneEsc
	PaneTrust // t
	PaneYes   // y
)

// scrollPage is how far a page key scrolls the detail view.
const scrollPage = 10

// Effects asking the shell to call relay.
type (
	RequestItems   struct{}
	RequestResolve struct{ Ref string }
	SendTrust      struct{ Path, Version, SHA256 string }
)

func (RequestItems) effect()   {}
func (RequestResolve) effect() {}
func (SendTrust) effect()      {}

// refRE finds a reference in model text: [[kind:id]].
var refRE = regexp.MustCompile(`\[\[(fact|doc|note|memo|journal|rem|file|web|shown|report):([^\[\]\n]+)\]\]`)

// ParseRefs is the distinct references in text, in order of first
// appearance, as written.
func ParseRefs(text string) []string {
	var out []string
	for _, m := range refRE.FindAllString(text, -1) {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// latestRefs is the references in the latest assistant message: the
// entries of the last agent entry's message id, or, without an id, the
// run of id-less agent entries it ends.
func latestRefs(entries []Entry) []string {
	j := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == Agent {
			j = i
			break
		}
	}
	if j < 0 {
		return nil
	}
	id := entries[j].ID
	var parts []string
	for i := j; i >= 0; i-- {
		e := entries[i]
		if id == "" && (e.Kind != Agent || e.ID != "") {
			break
		}
		if e.Kind == Agent && e.ID == id {
			parts = append(parts, e.Text)
		}
	}
	slices.Reverse(parts)
	return ParseRefs(strings.Join(parts, "\n"))
}

// LatestRefs is the references in the latest assistant message of s.
func LatestRefs(s State) []string { return latestRefs(s.Entries) }

// Cite is what the pane knows about ref: its citation, or an unchecked one.
func (p Pane) Cite(ref string) Citation {
	for _, c := range p.Cites {
		if c.Ref == ref {
			return c
		}
	}
	return Citation{Ref: ref}
}

var urlRE = regexp.MustCompile("(?i)(?:[a-z][a-z0-9+.\\-]*://|www\\.)[^\\s<>\"'`)\\]\\x1b]*")

// Hosts is the distinct hosts of the URLs in s, lowercased, without port
// or user, in order of appearance.
func Hosts(s string) []string {
	var out []string
	for _, u := range urlRE.FindAllString(s, -1) {
		if i := strings.Index(u, "://"); i >= 0 {
			u = u[i+3:]
		}
		if i := strings.IndexAny(u, "/?#"); i >= 0 {
			u = u[:i]
		}
		if i := strings.LastIndex(u, "@"); i >= 0 {
			u = u[i+1:]
		}
		h := u
		if x, err := url.Parse("//" + u); err == nil && x.Hostname() != "" {
			h = x.Hostname()
		}
		h = strings.ToLower(strings.TrimRight(h, ".,;:!?"))
		if h != "" && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// NewHosts is the hosts of the URLs in texts that no approved or trusted
// item has shown yet.
func (p Pane) NewHosts(texts ...string) []string {
	var out []string
	for _, t := range texts {
		for _, h := range Hosts(t) {
			if !slices.Contains(p.Domains, h) && !slices.Contains(out, h) {
				out = append(out, h)
			}
		}
	}
	return out
}

// addDomains adds the hosts of the URLs in texts to the approved set.
func (r *reducer) addDomains(texts ...string) {
	p := &r.s.Pane
	var add []string
	for _, t := range texts {
		for _, h := range Hosts(t) {
			if !slices.Contains(p.Domains, h) && !slices.Contains(add, h) {
				add = append(add, h)
			}
		}
	}
	if len(add) == 0 {
		return
	}
	d := append(append([]string(nil), p.Domains...), add...)
	sort.Strings(d)
	p.Domains = d
}

// refresh asks relay for the item list, unless it is known not to have one.
func (r *reducer) refresh() {
	if !r.s.Pane.Unavailable {
		r.emit(RequestItems{})
	}
}

// displays records what a tool call showed the operator.
func (r *reducer) displays(callID string, ds []Display) {
	if len(ds) == 0 {
		return
	}
	for _, d := range ds {
		it := Item{Kind: ShownItem, CallID: callID, Ref: d.Ref, Title: d.Title, Text: d.Body,
			Sources: d.Sources, Labels: d.Labels}
		r.putLocal(it)
	}
	r.refresh()
}

// putLocal adds an item to the client's own list, or replaces the one
// with its key.
func (r *reducer) putLocal(it Item) {
	p := &r.s.Pane
	for i := range p.Local {
		if p.Local[i].Key() == it.Key() {
			p.Local[i] = it
			return
		}
	}
	p.Local = append(p.Local, it)
}

// proposalAsked records a permission request as a proposal.
func (r *reducer) proposalAsked(pm Permission) {
	r.putLocal(Item{Kind: ProposalItem, CallID: pm.CallID, Tool: pm.Name, Title: pm.Title, Input: pm.Input,
		Preview: pm.Preview, Labels: pm.Shown().Union(r.s.Labels), Status: StatusWaitingProposal, AskID: pm.ID,
		FixedArgs: pm.FixedArgs})
	r.refresh()
}

// proposalAnswered sets the status of the proposal for request id.
func (r *reducer) proposalAnswered(id uint64, status string) {
	p := &r.s.Pane
	for i := range p.Local {
		if p.Local[i].Kind == ProposalItem && p.Local[i].AskID == id {
			p.Local[i].Status = status
		}
	}
}

// citeLatest asks relay about each reference in the latest reply that it
// has not been asked about.
func (r *reducer) citeLatest() {
	p := &r.s.Pane
	if p.Unavailable {
		return
	}
	for _, ref := range latestRefs(r.s.Entries) {
		if slices.ContainsFunc(p.Cites, func(c Citation) bool { return c.Ref == ref }) {
			continue
		}
		p.Cites = append(p.Cites, Citation{Ref: ref})
		r.emit(RequestResolve{Ref: ref})
	}
}

func (r *reducer) pane(e Event) {
	s := &r.s
	p := &s.Pane
	switch e := e.(type) {
	case ItemsLoaded:
		switch {
		case e.Unavailable:
			p.Unavailable, p.Relay, p.Server = true, false, nil
		case e.Failed:
			return
		default:
			p.Relay = true
			p.Server = append([]Item(nil), e.Items...)
		}
		r.keepSelection()
	case CitationResolved:
		if e.Failed {
			return
		}
		for i := range p.Cites {
			if p.Cites[i].Ref == e.Cite.Ref {
				c := e.Cite
				c.Ref = p.Cites[i].Ref
				p.Cites[i] = c
			}
		}
	case TrustDone:
		t := p.Trust
		if t == nil || t.Item.Path != e.Path {
			return
		}
		p.Trust = nil
		if e.Err != "" {
			p.Message = "Trust failed."
			p.Failure = e.Err
			p.Command = t.Item.TrustCommand
			return
		}
		p.Message = "Trusted."
		p.Failure, p.Command = "", ""
		r.addDomains(t.Item.Text)
		for _, src := range t.Item.Sources {
			r.addDomains(src.Ref)
		}
		r.refresh()
	case PaneKey:
		r.paneKey(e)
	}
}

// keepSelection keeps the same item selected when the list changes.
func (r *reducer) keepSelection() {
	p := &r.s.Pane
	items := PaneItems(r.s)
	i := SelectedIndex(r.s)
	if i < 0 {
		p.Sel, p.SelKey, p.Detail, p.Scroll = 0, "", false, 0
		return
	}
	p.Sel = i
	if p.SelKey != "" && items[i].Key() != p.SelKey {
		p.SelKey = items[i].Key() // it went away
	}
}

func (r *reducer) selectIndex(i int) {
	p := &r.s.Pane
	items := PaneItems(r.s)
	if len(items) == 0 {
		p.Sel, p.SelKey = 0, ""
		return
	}
	i = min(max(i, 0), len(items)-1)
	p.Sel, p.SelKey = i, items[i].Key()
}

func (r *reducer) paneKey(e PaneKey) {
	s := &r.s
	p := &s.Pane
	if e.Action == PaneToggle {
		if len(s.Asks) > 0 || (p.Trust != nil && p.Trust.Sent) {
			return
		}
		p.Trust, p.Message, p.Failure, p.Command = nil, "", "", ""
		p.Focus = !p.Focus
		if p.Focus {
			r.selectIndex(max(SelectedIndex(*s), 0))
		} else {
			p.Detail, p.Scroll = false, 0
		}
		return
	}
	if !p.Focus || len(s.Asks) > 0 {
		return
	}
	if t := p.Trust; t != nil {
		switch {
		case t.Sent:
		case e.Action == PaneYes:
			if e.At.Sub(t.Since) >= ArmDelay {
				p.Trust = &TrustPrompt{Item: t.Item, Since: t.Since, Sent: true}
				r.emit(SendTrust{Path: t.Item.Path, Version: t.Item.Version, SHA256: t.Item.SHA256})
			}
		default:
			p.Trust = nil
			p.Message = "Trust cancelled."
		}
		return
	}
	p.Message, p.Failure, p.Command = "", "", ""
	i := SelectedIndex(*s)
	switch e.Action {
	case PaneUp, PaneDown:
		d := 1
		if e.Action == PaneUp {
			d = -1
		}
		if p.Detail {
			p.Scroll = min(max(p.Scroll+d, 0), max(e.Max, 0))
		} else if i >= 0 {
			r.selectIndex(i + d)
		}
	case PagePrev, PageNext:
		if p.Detail {
			d := scrollPage
			if e.Action == PagePrev {
				d = -scrollPage
			}
			p.Scroll = min(max(p.Scroll+d, 0), max(e.Max, 0))
		}
	case PaneEnter:
		if i >= 0 && !p.Detail {
			p.Detail, p.Scroll = true, 0
		}
	case PaneEsc:
		if p.Detail {
			p.Detail, p.Scroll = false, 0
		} else {
			p.Focus = false
		}
	case PaneTrust:
		r.startTrust(e.At, i)
	}
}

// startTrust begins a trust of the selected item, or says why it cannot.
func (r *reducer) startTrust(at time.Time, i int) {
	p := &r.s.Pane
	if i < 0 {
		return
	}
	it := PaneItems(r.s)[i]
	switch {
	case it.Kind != ShownItem:
		p.Message = "Only shown items can be trusted. A proposal is approved when it asks."
	case !it.Labels.Contaminated:
		p.Message = "Nothing to trust: this item is not marked contaminated."
	case p.Relay && it.CanTrust && it.Path != "" && it.Version != "" && it.SHA256 != "":
		p.Trust = &TrustPrompt{Item: it, Since: at}
	case it.TrustCommand != "":
		p.Message = "Trust needs a terminal. Run this there:"
		p.Command = it.TrustCommand
	default:
		p.Message = "Trust is not available here. It needs relay chat."
	}
}
