package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/devcules/devcules/internal/config"
)

// healthTimeout bounds the check: health must answer fast or not at all.
const healthTimeout = 5 * time.Second

// statusGetter fetches a URL and reports its HTTP status code. It is a
// parameter so tests can fake answers without opening sockets.
type statusGetter func(url string) (int, error)

// httpStatus is the production statusGetter: one GET with a timeout.
func httpStatus(url string) (int, error) {
	client := &http.Client{Timeout: healthTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// healthURL is where this machine's serve process should answer.
func healthURL(cfg config.Config) string {
	return "http://" + cfg.Listen + "/healthz"
}

// runHealth probes the /healthz endpoint that M0.5 adds and reports whether
// this machine's serve process answers.
func runHealth(cfg config.Config, stdout, stderr io.Writer) int {
	return checkHealth(healthURL(cfg), httpStatus, stdout, stderr)
}

// checkHealth reports one probe result. Anything but a 2xx answer —
// including nothing listening at all — is exit 1 with the reason.
func checkHealth(url string, get statusGetter, stdout, stderr io.Writer) int {
	code, err := get(url)
	if err != nil {
		fmt.Fprintf(stderr, "not running: GET %s failed: %s\n", url, err)
		return exitError
	}
	if code < 200 || code > 299 {
		fmt.Fprintf(stderr, "not healthy: GET %s returned status %d\n", url, code)
		return exitError
	}
	fmt.Fprintf(stdout, "ok: %s\n", url)
	return exitOK
}
