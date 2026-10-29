package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/config"
)

// Hook exit codes: agents' adapters map them onto their own hook
// protocols. Anything else (1) is a failure, which adapters treat as
// deny.
const (
	hookAllow = 0
	hookDeny  = 2
)

// hookMaxWait bounds one hook run: an approval waits at most its
// timeout (10 minutes), plus room for a serve restart.
const hookMaxWait = 15 * time.Minute

// hookCall is what an agent's adapter writes to `devcules hook` on
// stdin: one tool call.
type hookCall struct {
	Agent        string `json:"agent"`
	Tool         string `json:"tool"`
	Command      string `json:"command"`
	Irreversible bool   `json:"irreversible"`
}

// hookAnswer is what serve answers and the hook prints.
type hookAnswer struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Approval *struct {
		ID       string `json:"id"`
		RuleName string `json:"ruleName"`
	} `json:"approval,omitempty"`
}

// hookClient is what runHook needs from HTTP; tests pass a fake clock
// through sleep.
type hookClient struct {
	http  *http.Client
	sleep func(context.Context, time.Duration) error
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runHook asks serve whether one tool call may run. It reads the call
// as JSON on stdin, sends it with the launch token
// (DEVCULES_LAUNCH_TOKEN) to DEVCULES_SERVE_URL, waits while a person
// decides, prints {"decision", "reason", "id"} on stdout and exits 0
// to allow or 2 to deny. While it waits it says so on stderr once.
// Network failures are retried until hookMaxWait; then, or on any
// other failure, it exits 1.
func runHook(cfg config.Config, stdin io.Reader, stdout, stderr io.Writer, c hookClient) int {
	if cfg.LaunchToken == "" {
		fmt.Fprintln(stderr, "devcules hook: DEVCULES_LAUNCH_TOKEN (or _FILE) is not set: Devcules sets it when it launches the agent")
		return exitError
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 80<<10))
	if err != nil {
		fmt.Fprintln(stderr, "devcules hook: cannot read stdin:", err)
		return exitError
	}
	var call hookCall
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&call); err != nil || call.Tool == "" {
		fmt.Fprintln(stderr, `devcules hook: stdin must be {"tool": "...", "command": "...", "irreversible": false, "agent": "..."}`)
		return exitError
	}
	body, _ := json.Marshal(call)
	ctx, cancel := context.WithTimeout(context.Background(), hookMaxWait)
	defer cancel()
	base := strings.TrimRight(cfg.ServeURL, "/")

	ans, err := hookRequest(ctx, c, cfg.LaunchToken, http.MethodPost, base+"/hook/approvals?wait=0", body)
	told := false
	for err == nil && ans.Decision == "ask" && ans.Approval != nil {
		if !told {
			what := "this step"
			if ans.Approval.RuleName != "" {
				what = ans.Approval.RuleName
			}
			fmt.Fprintf(stderr, "devcules: %s needs approval; answer it in Devcules on your phone\n", what)
			told = true
		}
		ans, err = hookRequest(ctx, c, cfg.LaunchToken, http.MethodGet, base+"/hook/approvals/"+url.PathEscape(ans.Approval.ID)+"?wait=25", nil)
	}
	if err != nil {
		fmt.Fprintln(stderr, "devcules hook:", err)
		return exitError
	}
	out := map[string]string{"decision": ans.Decision, "reason": ans.Reason}
	if ans.Approval != nil {
		out["id"] = ans.Approval.ID
	}
	line, _ := json.Marshal(out)
	fmt.Fprintln(stdout, string(line))
	switch ans.Decision {
	case "allow":
		return hookAllow
	case "deny":
		return hookDeny
	}
	fmt.Fprintf(stderr, "devcules hook: serve answered %q\n", ans.Decision)
	return exitError
}

// hookRequest sends one request, retrying transport errors and 5xx
// answers (serve restarting) with backoff until ctx ends. A 4xx is
// final: its message is returned.
func hookRequest(ctx context.Context, c hookClient, token, method, target string, body []byte) (hookAnswer, error) {
	backoff := time.Second
	for {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, rd)
		if err != nil {
			return hookAnswer{}, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		var retry error
		if err != nil {
			var uerr *url.Error
			if errors.As(err, &uerr) {
				err = uerr.Err
			}
			retry = fmt.Errorf("cannot reach serve: %w", err)
		} else {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusOK:
				var ans hookAnswer
				if err := json.Unmarshal(data, &ans); err != nil {
					return hookAnswer{}, fmt.Errorf("serve sent a bad answer")
				}
				return ans, nil
			case resp.StatusCode >= 500:
				retry = fmt.Errorf("serve answered %d", resp.StatusCode)
			default:
				var e struct {
					Error string `json:"error"`
				}
				_ = json.Unmarshal(data, &e)
				return hookAnswer{}, fmt.Errorf("serve refused the hook (%d): %s", resp.StatusCode, e.Error)
			}
		}
		if err := c.sleep(ctx, backoff); err != nil {
			return hookAnswer{}, fmt.Errorf("%w (gave up after %s)", retry, hookMaxWait)
		}
		backoff = min(backoff*2, 15*time.Second)
	}
}
