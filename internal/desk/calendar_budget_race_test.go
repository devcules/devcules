//go:build race

package desk

import "time"

// Instrumentation and unrelated parallel package tests can exceed the
// ordinary budget by more than 3×. An unbounded walk remains far slower.
const b8FarWindowTimeBudget = 500 * time.Millisecond
