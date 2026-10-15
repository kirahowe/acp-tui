// Package wire translates ACP messages into core events. It is pure: the
// shell passes in the time, and nothing here does IO.
package wire

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
)

// Capabilities is what acp-tui tells the agent it supports: notices, and
// no file system or terminal access.
func Capabilities() acp.ClientCapabilities {
	return acp.ClientCapabilities{
		Session: &acp.ClientSessionCapabilities{Notices: &acp.NoticeCapabilities{}},
	}
}

// Connected describes the session once initialize and session/new have
// answered: the agent's name, title and version from agentInfo, and the
// current model when the session has a config option of category model.
func Connected(init acp.InitializeResponse, sess acp.NewSessionResponse, at time.Time) core.Event {
	c := core.Connected{At: at, Model: model(sess.ConfigOptions), Embedded: init.AgentCapabilities.PromptCapabilities.EmbeddedContext}
	if info := init.AgentInfo; info != nil {
		c.Agent, c.Version = oneLine(info.Name), oneLine(info.Version)
		if info.Title != nil {
			c.Title = oneLine(*info.Title)
		}
	}
	return c
}

// PromptBlocks is the content of a session/prompt: the text, then each
// paste as an embedded text resource, uri paste:N, mime type text/plain.
func PromptBlocks(text string, atts []core.Attachment) []acp.ContentBlock {
	blocks := []acp.ContentBlock{{Text: &acp.ContentBlockText{Type: "text", Text: text}}}
	for _, a := range atts {
		mime := "text/plain"
		blocks = append(blocks, acp.ResourceBlock(acp.EmbeddedResourceResource{
			TextResourceContents: &acp.TextResourceContents{Uri: a.URI, MimeType: &mime, Text: a.Text},
		}))
	}
	return blocks
}

// model is the display name of the current value of the first config
// option of category model, or "".
func model(opts []acp.SessionConfigOption) string {
	for _, o := range opts {
		sel := o.Select
		if sel == nil || sel.Category == nil || *sel.Category != acp.SessionConfigOptionCategoryModel {
			continue
		}
		var choices []acp.SessionConfigSelectOption
		if g := sel.Options.Ungrouped; g != nil {
			choices = *g
		}
		if g := sel.Options.Grouped; g != nil {
			for _, grp := range *g {
				choices = append(choices, grp.Options...)
			}
		}
		for _, c := range choices {
			if c.Value == sel.CurrentValue && c.Name != "" {
				return oneLine(c.Name)
			}
		}
		return oneLine(string(sel.CurrentValue))
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Update translates one session/update notification.
func Update(n acp.SessionNotification, at time.Time) core.Event {
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil:
		c := u.AgentMessageChunk
		return core.TextChunk{At: at, MessageID: messageID(c.MessageId), Text: blockText(c.Content)}
	case u.AgentThoughtChunk != nil:
		c := u.AgentThoughtChunk
		return core.ThoughtChunk{At: at, MessageID: messageID(c.MessageId), Text: blockText(c.Content)}
	case u.ToolCall != nil:
		c := u.ToolCall
		e := core.ToolStarted{At: at, ID: string(c.ToolCallId), Title: c.Title, Kind: string(c.Kind),
			Status: toolStatus(c.Status), Input: rawJSON(c.RawInput)}
		if c.Name != nil {
			e.Name = *c.Name
		}
		if c.Content != nil {
			// A call can arrive already finished, with its output.
			e.Output = contentText(c.Content)
		}
		return e
	case u.ToolCallUpdate != nil:
		c := u.ToolCallUpdate
		e := core.ToolUpdated{At: at, ID: string(c.ToolCallId), Title: c.Title}
		if c.Status != nil {
			st := toolStatus(*c.Status)
			e.Status = &st
		}
		if c.RawInput != nil {
			in := rawJSON(c.RawInput)
			e.Input = &in
		}
		if c.Content != nil {
			out := contentText(c.Content)
			e.Output = &out
		}
		return e
	case u.UsageUpdate != nil:
		e := core.Usage{At: at, Used: u.UsageUpdate.Used, Size: u.UsageUpdate.Size}
		if c := u.UsageUpdate.Cost; c != nil {
			e.Cost = &core.Cost{Amount: c.Amount, Currency: c.Currency}
		}
		return e
	case u.Notice != nil:
		c := u.Notice
		e := core.Notice{At: at, Severity: string(c.Severity), Title: c.Title, RetryAt: retryAt(c.Meta)}
		if c.Description != nil {
			e.Detail = *c.Description
		}
		return e
	}
	return core.Ignored{At: at}
}

// Response translates the result of session/prompt.
func Response(r acp.PromptResponse, err error, at time.Time) core.Event {
	if err == nil {
		return core.PromptDone{At: at, Stop: core.StopReason(r.StopReason)}
	}
	var re *acp.RequestError
	if !errors.As(err, &re) {
		return core.PromptFailed{At: at, Message: err.Error()}
	}
	msg := re.Message
	var data struct {
		Message string `json:"message"`
	}
	if b, err := json.Marshal(re.Data); err == nil && json.Unmarshal(b, &data) == nil &&
		data.Message != "" && !strings.Contains(msg, data.Message) {
		msg += ": " + data.Message
	}
	return core.PromptFailed{At: at, Message: msg}
}

// Permission translates a session/request_permission. id is the shell's
// handle for answering it.
func Permission(req acp.RequestPermissionRequest, id uint64, at time.Time) core.PermissionAsked {
	p := core.Permission{ID: id, Input: rawJSON(req.ToolCall.RawInput)}
	if n := req.ToolCall.Name; n != nil {
		p.Name = *n
	}
	if t := req.ToolCall.Title; t != nil {
		p.Title = *t
	}
	for _, o := range req.Options {
		p.Options = append(p.Options, core.PermOption{ID: string(o.OptionId), Name: o.Name, Kind: core.OptionKind(o.Kind)})
	}
	return core.PermissionAsked{At: at, Permission: p}
}

// Outcome is the reply to a session/request_permission: the chosen
// option, or cancelled.
func Outcome(a core.AnswerPermission) acp.RequestPermissionResponse {
	if a.Cancelled {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Cancelled: &acp.RequestPermissionOutcomeCancelled{Outcome: "cancelled"},
		}}
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(a.OptionID), Outcome: "selected"},
	}}
}

// labelsKey is where agentloop puts the session's labels in _meta.
const labelsKey = "agentloop/labels"

// Labels reads the session's labels from the _meta of a tool_call,
// tool_call_update or usage_update. ok is false when the update carries
// none, or they are malformed. A label this client does not know shows
// nowhere: the agent is the one that fails closed on it.
func Labels(n acp.SessionNotification, at time.Time) (core.LabelsSeen, bool) {
	u := n.Update
	var meta map[string]json.RawMessage
	switch {
	case u.ToolCall != nil:
		meta = u.ToolCall.Meta
	case u.ToolCallUpdate != nil:
		meta = u.ToolCallUpdate.Meta
	case u.UsageUpdate != nil:
		meta = u.UsageUpdate.Meta
	}
	var names []string
	if json.Unmarshal(meta[labelsKey], &names) != nil {
		return core.LabelsSeen{}, false
	}
	e := core.LabelsSeen{At: at}
	for _, name := range names {
		switch name {
		case "private":
			e.Labels.Private = true
		case "contaminated":
			e.Labels.Contaminated = true
		}
	}
	return e, true
}

func messageID(id *acp.MessageId) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

func toolStatus(s acp.ToolCallStatus) core.ToolStatus {
	switch s {
	case acp.ToolCallStatusInProgress:
		return core.Running
	case acp.ToolCallStatusCompleted:
		return core.OK
	case acp.ToolCallStatusFailed:
		return core.Failed
	}
	return core.Pending
}

// blockText is a content block as text; anything but text becomes a
// placeholder.
func blockText(b acp.ContentBlock) string {
	switch {
	case b.Text != nil:
		return b.Text.Text
	case b.Image != nil:
		return "[image]"
	case b.Audio != nil:
		return "[audio]"
	case b.ResourceLink != nil:
		return "[resource: " + b.ResourceLink.Uri + "]"
	case b.Resource != nil:
		r := b.Resource.Resource
		switch {
		case r.TextResourceContents != nil:
			return "[resource: " + r.TextResourceContents.Uri + "]"
		case r.BlobResourceContents != nil:
			return "[resource: " + r.BlobResourceContents.Uri + "]"
		}
		return "[resource]"
	}
	return ""
}

// firstLine is the first non-empty line of a tool call's text content.
// contentText is the text of a call's content blocks, one after another.
// Diffs and terminals are not shown.
func contentText(content []acp.ToolCallContent) string {
	var parts []string
	for _, c := range content {
		if c.Content != nil && c.Content.Content.Text != nil {
			parts = append(parts, c.Content.Content.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// rawJSON is a call's raw input or output as compact JSON, or "" when
// there is none or it cannot be encoded.
func rawJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return ""
	}
	return string(b)
}

// retryAt reads agentloop's retry time from a notice's _meta. Any other
// agent, or a malformed value, gives nil: no countdown.
func retryAt(meta map[string]json.RawMessage) *time.Time {
	var retry struct {
		RetryAt string `json:"retry_at"`
	}
	if json.Unmarshal(meta["agentloop/retry"], &retry) != nil {
		return nil
	}
	at, err := time.Parse(time.RFC3339, retry.RetryAt)
	if err != nil {
		return nil
	}
	return &at
}
