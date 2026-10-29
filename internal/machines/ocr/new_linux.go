//go:build linux

package ocr

// New returns the tesseract driver on Linux.
func New() (Driver, error) { return newTesseract() }
