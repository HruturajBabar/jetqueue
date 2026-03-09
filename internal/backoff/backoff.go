package backoff

import "time"

// Compute returns the retry delay for the given attempt using
// exponential backoff with a cap.
//
// attempt = 0 → 2s
// attempt = 1 → 4s
// attempt = 2 → 8s
// ...
// capped at 60s
func Compute(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	base := 2 * time.Second
	max := 60 * time.Second

	delay := base * time.Duration(1<<attempt)

	if delay > max {
		return max
	}

	return delay
}
