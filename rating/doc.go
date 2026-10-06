// Package rating calculates a 0–100 financial-health rating from nine credit-history
// indicators.
//
// Rate classifies deals, measures the indicators, applies scoring curves and weights, and
// caps the result when required. Flags, lender statistics and monitoring subscriptions
// are returned separately and do not affect the score.
//
// The model supports financial education rather than lending decisions. Its rules are
// documented in docs/specs.
package rating
