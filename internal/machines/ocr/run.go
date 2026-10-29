package ocr

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"
)

// command is the swappable runner; see elements.run for the shape.
type command func(ctx context.Context, name string, args ...string) ([]byte, error)

// runCommand is the production command: stdout captured, stderr
// wrapped into the error.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if errb.Len() > 0 {
			return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(errb.String()))
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out.Bytes(), nil
}

// writePNG encodes a frame to PNG bytes for engines that read files,
// into the caller's private dir.
func writePNG(dir, name string, frame image.Image) (string, error) {
	p := fmt.Sprintf("%s/%s", dir, name)
	f, err := os.Create(p)
	if err != nil {
		return "", fmt.Errorf("ocr: temp png: %w", err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<16)
	if err := png.Encode(w, frame); err != nil {
		return "", fmt.Errorf("ocr: encode png: %w", err)
	}
	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("ocr: write png: %w", err)
	}
	return p, nil
}
