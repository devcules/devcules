package approvals

import (
	"bytes"
	"log/slog"
	"sync"
)

type lockedWriter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func slogTo(b *bytes.Buffer, mu *sync.Mutex) *slog.Logger {
	return slog.New(slog.NewTextHandler(lockedWriter{b, mu}, nil))
}
