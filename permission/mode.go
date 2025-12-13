// ABOUTME: Defines permission modes - Auto (allow all), Ask (prompt user),
// ABOUTME: and Deny (block all) for controlling tool execution.
package permission

// Mode defines how permissions are handled.
type Mode string

const (
	ModeAuto Mode = "auto"
	ModeAsk  Mode = "ask"
	ModeDeny Mode = "deny"
)
