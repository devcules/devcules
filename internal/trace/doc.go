// Package trace keeps a trace per agent launch: every model call the
// router forwarded for that launch's token, with the question, the
// reply, the tool calls the model asked for, token counts and what was
// redacted.
//
// Traces hold text as the provider saw it, so a masked secret appears
// as its placeholder, never its value (a type in log mode was sent,
// and is stored, as written). Text fields are capped (32 KiB for the
// question and the reply, 8 KiB per tool call's arguments, 64 tool
// calls) and calls older than the retention (DEVCULES_TRACE_DAYS,
// 30 by default) are pruned.
//
// The router produces the records (internal/router's observer); this
// package stores, prunes and reads them. Reading a trace is audited by
// the API, not here.
package trace
