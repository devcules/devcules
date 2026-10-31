package cli

import (
	"context"

	"github.com/devcules/devcules/plugins/agents"
)

// NewOpenClaw is the OpenClaw assistant CLI (id "openclaw"). It
// announces itself on `openclaw --version`.
func NewOpenClaw() Agent {
	return Must(New("openclaw",
		WithName("OpenClaw"),
		WithArgs("chat", "--cli"),
		WithInstallHint("curl -fsSL https://openclaw.ai/install.sh | bash"),
		WithDetect(func(ctx context.Context) (string, bool) {
			out, ok := agents.Version(ctx, nil, "openclaw", "--version")
			if !ok {
				return "", false
			}
			return agents.SemVer(out), true
		})),
	)
}
