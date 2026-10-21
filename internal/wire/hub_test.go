package wire

import (
	"testing"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
)

func TestUserMessageChunkIsAnEvent(t *testing.T) {
	id := acp.MessageId("m1")
	n := acp.SessionNotification{SessionId: "s", Update: acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{
		Content: acp.TextBlock("what's new"), MessageId: &id}}}
	got, ok := Update(n, t0).(core.UserChunk)
	if !ok || got.Text != "what's new" || got.MessageID != "m1" {
		t.Fatalf("%#v", Update(n, t0))
	}
}
