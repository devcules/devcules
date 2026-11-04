package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/desk"
	"github.com/devcules/devcules/internal/push"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/vault"
)

// setupApprovals loads the VAPID key (making it on first start) and
// the approval settings, and starts the expiry loop until ctx ends.
func setupApprovals(ctx context.Context, cfg config.Config, s store.Store, v *vault.Vault, auditLog *audit.Log,
	redactor *redact.Redactor, log *slog.Logger) (*approvals.Service, *push.Service, error) {
	p, err := push.New(ctx, push.Options{Store: s, Keys: v, Contact: cfg.PushSubject(), Log: log, Client: pushClient()})
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

// pushClient sends Web Push messages. A subscription endpoint is a URL
// a browser session hands in, so its host must resolve only to public
// addresses (no loopback, private or link-local), as a user's feed URL
// must; like push's default client it never follows a redirect and
// gives up after 10 seconds. It honours HTTP(S)_PROXY / NO_PROXY.
func pushClient() *http.Client {
	return newPushClient(http.ProxyFromEnvironment)
}

// newPushClient builds pushClient around a proxy chooser. A request
// that goes direct dials through the desk's guarded dialer (every
// resolved address checked, the validated one dialed). A request that
// goes through a proxy has its target checked first; the proxy itself,
// which the operator configured and which is often on a private
// address (a corporate or VPC proxy), is dialed without the rule.
func newPushClient(proxy func(*http.Request) (*url.URL, error)) *http.Client {
	direct := desk.NewClient(10 * time.Second).Transport.(*http.Transport).Clone()
	direct.Proxy = nil
	viaProxy := direct.Clone()
	viaProxy.Proxy = proxy
	viaProxy.DialContext = (&net.Dialer{Timeout: 8 * time.Second}).DialContext
	return &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &pushTransport{proxy: proxy, direct: direct, viaProxy: viaProxy},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// pushTransport routes each request to the direct (guarded) transport,
// or, when the proxy chooser names a proxy, checks the target's
// addresses and hands it to the proxy transport. viaProxy shares the
// chooser, so it only ever dials the proxy.
type pushTransport struct {
	proxy            func(*http.Request) (*url.URL, error)
	direct, viaProxy http.RoundTripper
}

func (t *pushTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p, err := t.proxy(req)
	if err == nil && p == nil {
		return t.direct.RoundTrip(req)
	}
	if err == nil {
		_, err = desk.PublicAddrs(req.Context(), req.URL.Hostname())
	}
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return t.viaProxy.RoundTrip(req)
}
