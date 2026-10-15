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
		return core.ToolStarted{At: at, ID: string(c.ToolCallId), Title: c.Title, Kind: string(c.Kind), Status: toolStatus(c.Status)}
	case u.ToolCallUpdate != nil:
		c := u.ToolCallUpdate
		e := core.ToolUpdated{At: at, ID: string(c.ToolCallId), Title: c.Title}
		if c.Status != nil {
			st := toolStatus(*c.Status)
			e.Status = &st
			if st == core.Failed {
				if line := firstLine(c.Content); line != "" {
					e.Text = &line
				}
			}
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

// ChoosePermission refuses a permission request, since the MVP cannot ask
// the user, and returns a notice so the user sees what was refused. It
// picks the first reject_once option, else reject_always, else cancels.
func ChoosePermission(req acp.RequestPermissionRequest, at time.Time) (acp.RequestPermissionResponse, core.Event) {
	title := string(req.ToolCall.ToolCallId)
	if req.ToolCall.Title != nil && *req.ToolCall.Title != "" {
		title = *req.ToolCall.Title
	}
	notice := core.Notice{At: at, Severity: "warning", Title: "denied: " + title + " (acp-tui cannot ask yet)"}
	for _, kind := range []acp.PermissionOptionKind{acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways} {
		for _, o := range req.Options {
			if o.Kind == kind {
				return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId, Outcome: "selected"},
				}}, notice
			}
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Cancelled: &acp.RequestPermissionOutcomeCancelled{Outcome: "cancelled"},
	}}, notice
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
func firstLine(content []acp.ToolCallContent) string {
	for _, c := range content {
		if c.Content == nil || c.Content.Content.Text == nil {
			continue
		}
		for line := range strings.Lines(c.Content.Content.Text.Text) {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return ""
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
