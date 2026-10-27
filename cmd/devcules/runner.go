package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/devcules/devcules/internal/config"
)

// runRunner idles until a signal: the runner has nothing to drive until
// the link protocol lands in M1.3. A vault key refuses first — runners
// get per-launch tokens, never real keys.
func runRunner(cfg config.Config, stdout, stderr io.Writer, sigs <-chan os.Signal) int {
	if _, err := cfg.ForRole("runner"); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return exitError
	}
	log := slog.New(slog.NewJSONHandler(stdout, nil))
	log.Info("runner idle: waiting for the link (M1.3)")
	<-sigs
	return exitOK
}
