package panel

import "time"

// SetSleep replaces the controller's retry-delay seam so acceptance tests can record the retry
// interval and avoid real wall-clock sleeps (Constitution II). Test-only (export_test.go).
func (c *Controller) SetSleep(f func(time.Duration)) { c.sleep = f }
