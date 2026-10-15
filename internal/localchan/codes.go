package localchan

// Exit codes of the CLI (docs/cli/convencoes.md#códigos-de-saída).
const (
	ExitOK          = 0
	ExitInternal    = 1
	ExitUsage       = 2
	ExitDenied      = 3
	ExitNotFound    = 4
	ExitUnavailable = 5
	ExitTimeout     = 6
	ExitConflict    = 7
	ExitLimit       = 8
	ExitPending     = 9
)

// Error codes the channel itself produces. The commands have more, stable
// and more specific than the families; a response with a code this table
// does not know carries its own "exit".
const (
	// Families (docs/cli/convencoes.md): a code may be just the family.
	CodeInternal    = "internal"
	CodeUsage       = "usage"
	CodeDenied      = "denied"
	CodeNotFound    = "not_found"
	CodeUnavailable = "unavailable"
	CodeTimeout     = "timeout"
	CodeConflict    = "conflict"
	CodeLimit       = "limit"
	CodePending     = "pending"

	CodeBadRequest         = "bad_request"
	CodeTooLarge           = "too_large"
	CodeNotIdentified      = "not_identified"
	CodeProtocolMismatch   = "protocol_mismatch"
	CodeEngineUnreachable  = "engine_unreachable"
	CodeCitadelUnavailable = "citadel_unavailable"
	CodeCitadelInFailover  = "citadel_in_failover"
)

var exitByCode = map[string]int{
	CodeInternal:    ExitInternal,
	CodeUsage:       ExitUsage,
	CodeDenied:      ExitDenied,
	CodeNotFound:    ExitNotFound,
	CodeUnavailable: ExitUnavailable,
	CodeTimeout:     ExitTimeout,
	CodeConflict:    ExitConflict,
	CodeLimit:       ExitLimit,
	CodePending:     ExitPending,

	// The codes of the channel.
	CodeBadRequest:         ExitInternal, // a bug of the CLI
	CodeTooLarge:           ExitLimit,
	CodeNotIdentified:      ExitDenied,
	CodeProtocolMismatch:   ExitUnavailable,
	CodeEngineUnreachable:  ExitUnavailable,
	CodeCitadelUnavailable: ExitUnavailable,
	CodeCitadelInFailover:  ExitUnavailable,

	// A few commands' codes the table of docs/cli/convencoes.md lists.
	"level_denied":        ExitDenied,
	"class_denied":        ExitDenied,
	"not_connected":       ExitDenied,
	"secret_detected":     ExitDenied,
	"pinned":              ExitDenied,
	"permission_prompt":   ExitDenied,
	"not_visible":         ExitNotFound,
	"app_required":        ExitUsage,
	"from_outside_tmp":    ExitUsage,
	"invalid_class":       ExitUsage,
	"invalid_schedule":    ExitUsage,
	"forbidden":           ExitDenied,
	"in_use":              ExitConflict,
	"approval_required":   ExitPending,
	"no_capable_machine":  ExitNotFound,
	"machine_unreachable": ExitUnavailable,
	"busy":                ExitConflict,
	"scheduled":           ExitPending,

	// The codes of the commands of communication and coordination.
	"name_taken":           ExitConflict,
	"no_active_task":       ExitConflict,
	"nothing_to_reply":     ExitConflict,
	"no_return_step":       ExitConflict,
	"rev_conflict":         ExitConflict,
	"edit_ambiguous":       ExitConflict,
	"invalid_state":        ExitConflict,
	"already_running":      ExitConflict,
	"edit_not_found":       ExitNotFound,
	"invalid_quest":        ExitUsage,
	"table_required":       ExitUsage,
	"different_tables":     ExitUsage,
	"ceiling_denied":       ExitDenied,
	"operator_denied":      ExitDenied,
	"automatic_connection": ExitDenied,
	"waiting_slot":         ExitPending,
	"approval_pending":     ExitPending,
}

// ExitFor is the exit code of an error code: the code of the family it
// belongs to, or 1 for one nobody knows.
func ExitFor(code string) int {
	if n, ok := exitByCode[code]; ok {
		return n
	}
	return ExitInternal
}
