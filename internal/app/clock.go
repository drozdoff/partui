package app

import "time"

// Clock returns the current time. It exists so tests can inject a fixed
// time and make lastVerified behaviour deterministic; production code uses
// time.Now via App.now.
type Clock func() time.Time
