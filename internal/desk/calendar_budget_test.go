//go:build !race

package desk

import "time"

// b8FarWindowTimeBudget bounds the far-future expansion regression. The
// unbounded schedule takes seconds; this catches that failure directly.
const b8FarWindowTimeBudget = 100 * time.Millisecond
