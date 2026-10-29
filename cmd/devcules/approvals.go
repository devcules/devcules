package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/push"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/vault"
)

// setupApprovals loads the VAPID key (making it on first start) and
// the approval settings, and starts the expiry loop until ctx ends.
func setupApprovals(ctx context.Context, cfg config.Config, s store.Store, v *vault.Vault, auditLog *audit.Log,
	redactor *redact.Redactor, log *slog.Logger) (*approvals.Service, *push.Service, error) {
	p, err := push.New(ctx, push.Options{Store: s, Keys: v, Contact: cfg.PushSubject(), Log: log})
	if err != nil {
		return nil, nil, err
	}
	if p.Created() {
		log.Info("push key created", "secret", push.KeySecret)
	}
	a, err := approvals.New(ctx, approvals.Options{Store: s, AuditLog: auditLog, Push: p, Redact: redactor, Log: log})
	if err != nil {
		return nil, nil, err
	}
	// Approvals left pending by an earlier run expire on schedule.
	a.Expire(ctx)
	go a.Run(ctx, 30*time.Second)
	return a, p, nil
}
