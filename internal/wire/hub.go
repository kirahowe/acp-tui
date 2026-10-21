package wire

import (
	"encoding/json"
	"encoding/json/jsontext"
	"strconv"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/strictjson"
)

// What relay's hub adds to ACP (see relay's wire contract, section 4).
const (
	hubKey = "relay/hub"
	// roleKey is the _meta key on session/new that names the role.
	roleKey = "relay/role"

	MethodRoles        = "_relay/roles"
	MethodRolesChanged = "_relay/roles/changed"
	MethodSettled      = "_relay/permission/settled"
	MethodEnd          = "_relay/conversations/end"
	MethodSeen         = "_relay/seen"
)

// IsHub says initialize answered with _meta["relay/hub"], a positive
// version number: the agent is relay's hub and may be asked for the
// roster. Anything else is plain ACP.
func IsHub(init acp.InitializeResponse) bool {
	v := jsontext.Value(init.Meta[hubKey])
	if v.Kind() != '0' {
		return false
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	return err == nil && n >= 1
}

// HubConnected describes the hub once initialize has answered.
func HubConnected(init acp.InitializeResponse, at time.Time) core.HubConnected {
	c := core.HubConnected{At: at, Embedded: init.AgentCapabilities.PromptCapabilities.EmbeddedContext}
	if info := init.AgentInfo; info != nil {
		c.Agent, c.Version = oneLine(info.Name), oneLine(info.Version)
		if info.Title != nil {
			c.Title = oneLine(*info.Title)
		}
	}
	return c
}

// RoleMeta is the _meta of a session/new that names the role.
func RoleMeta(role string) map[string]json.RawMessage {
	b, _ := json.Marshal(role)
	return map[string]json.RawMessage{roleKey: b}
}

type rawRole struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Labels       []string `json:"labels"`
	Working      bool     `json:"working"`
	Holds        int      `json:"holds"`
	Conversation *struct {
		SessionID    string `json:"sessionId"`
		OpenedAt     string `json:"openedAt"`
		LastActivity string `json:"lastActivity"`
		Turn         string `json:"turn"`
		Turns        int    `json:"turns"`
		Unread       bool   `json:"unread"`
		Approvals    int    `json:"approvals"`
	} `json:"conversation"`
}

// Roster translates the answer to _relay/roles. A failed call gives a
// RosterUpdate with Err.
func Roster(raw json.RawMessage, err error, at time.Time) core.RosterUpdate {
	if err != nil {
		return core.RosterUpdate{At: at, Err: errText(err)}
	}
	return roster(raw, at)
}

// RosterChanged translates _relay/roles/changed. ok is false when it
// cannot be read.
func RosterChanged(params json.RawMessage, at time.Time) (core.RosterUpdate, bool) {
	u := roster(params, at)
	return u, u.Err == ""
}

func roster(raw json.RawMessage, at time.Time) core.RosterUpdate {
	var r struct {
		Roles []rawRole `json:"roles"`
	}
	if strictjson.Unmarshal(raw, &r) != nil {
		return core.RosterUpdate{At: at, Err: "unreadable roster"}
	}
	u := core.RosterUpdate{At: at, Roles: []core.Role{}}
	seen := map[string]bool{}
	for _, x := range r.Roles {
		if x.ID == "" || seen[x.ID] {
			continue // a role is named once
		}
		seen[x.ID] = true
		role := core.Role{ID: x.ID, Title: x.Title, Labels: labelNames(x.Labels), Working: x.Working, Holds: max(x.Holds, 0)}
		if role.Title == "" {
			role.Title = x.ID
		}
		if c := x.Conversation; c != nil && c.SessionID != "" {
			rc := &core.RosterConv{Session: c.SessionID, Turn: core.TurnState(c.Turn), Turns: max(c.Turns, 0),
				Unread: c.Unread, Approvals: max(c.Approvals, 0)}
			rc.OpenedAt, _ = time.Parse(time.RFC3339Nano, c.OpenedAt)
			rc.LastActivity, _ = time.Parse(time.RFC3339Nano, c.LastActivity)
			role.Conv = rc
		}
		u.Roles = append(u.Roles, role)
	}
	return u
}

// Settled translates _relay/permission/settled. ok is false when it
// cannot be read.
func Settled(params json.RawMessage, at time.Time) (core.PermissionSettled, bool) {
	var r struct {
		SessionID  string          `json:"sessionId"`
		ToolCallID string          `json:"toolCallId"`
		Outcome    json.RawMessage `json:"outcome"`
	}
	if strictjson.Unmarshal(params, &r) != nil || r.SessionID == "" || r.ToolCallID == "" {
		return core.PermissionSettled{}, false
	}
	return core.PermissionSettled{At: at, Session: r.SessionID, CallID: r.ToolCallID, Outcome: string(r.Outcome)}, true
}

// Attached translates the answer to an attach.
func Attached(role, session string, err error, at time.Time) core.Attached {
	if err != nil {
		return core.Attached{At: at, Role: role, Session: session, Err: errText(err)}
	}
	return core.Attached{At: at, Role: role, Session: session}
}

// Ended translates the answer to _relay/conversations/end.
func Ended(session string, err error, at time.Time) core.ConversationEnded {
	e := core.ConversationEnded{At: at, Session: session}
	if err != nil {
		e.Err = errText(err)
	}
	return e
}

// SessionParams are the parameters of _relay/conversations/end.
func SessionParams(session string) map[string]any { return map[string]any{"sessionId": session} }

// SeenParams are the parameters of _relay/seen.
func SeenParams(session string, turn int) map[string]any {
	return map[string]any{"sessionId": session, "turn": turn}
}
