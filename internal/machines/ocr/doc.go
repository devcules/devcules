// Package ocr reads words off a captured frame: a text-only model can
// still find the Pay button because OCR gives it the words and their
// boxes (M4.3). macOS uses Vision, Linux tesseract, Windows the WinRT
// OCR engine — all behind one Driver, no new Go dependencies.
//
// It does no HTTP and no model calls. Read text is untrusted screen
// content: consumers must fence it before it reaches a model (PLAN §4).
package ocr
