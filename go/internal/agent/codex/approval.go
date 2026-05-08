package codex

import (
	"encoding/json"
	"fmt"
)

// handleApproval processes an approval request from the Codex server.
// Under "auto" policy, it always approves. Under other policies it may deny.
func handleApproval(policy string, msg *Message, enc *Encoder) error {
	var id int
	if msg.ID != nil {
		if err := json.Unmarshal(*msg.ID, &id); err != nil {
			return fmt.Errorf("codex: parse approval request ID: %w", err)
		}
	}

	decision := "approve"
	if policy != "auto" {
		// For non-auto policies, deny by default in automated mode.
		// Future: add human-in-the-loop approval support.
		decision = "deny"
	}

	resp, err := Response(id, ApprovalDecision{Decision: decision})
	if err != nil {
		return fmt.Errorf("codex: build approval response: %w", err)
	}
	if err := enc.Encode(resp); err != nil {
		return fmt.Errorf("codex: send approval response: %w", err)
	}

	return nil
}
