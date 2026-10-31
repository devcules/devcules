package cli

import (
	"context"

	"github.com/devcules/devcules/plugins/agents"
)

// NewHermes is the Hermes assistant CLI (id "hermes"). It announces
// itself on `hermes --version`.
func NewHermes() Agent {
	return Must(New("hermes",
		WithName("Hermes"),
		WithArgs("chat", "--cli"),
		WithInstallHint("curl -fsSL https://hermes.sh/install.sh | bash"),
		WithDetect(func(ctx context.Context) (string, bool) {
			out, ok := agents.Version(ctx, nil, "hermes", "--version")
			if !ok {
				return "", false
			}
			return agents.SemVer(out), true
		})),
	)
}
