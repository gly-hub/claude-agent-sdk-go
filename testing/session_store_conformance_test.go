package testing

import (
	stdtesting "testing"

	sdk "github.com/gly-hub/claude-agent-sdk-go"
)

func TestRunSessionStoreConformance(t *stdtesting.T) {
	RunSessionStoreConformance(t, func() sdk.SessionStore {
		return sdk.NewInMemorySessionStore()
	})
}
