// Maps battle service errors to the stable API error envelope, so every battle route
// answers the same failure with the same status and code.

package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/twh09/autonomous_battlefield/backend/internal/apierror"
	"github.com/twh09/autonomous_battlefield/backend/internal/authn"
	battleservice "github.com/twh09/autonomous_battlefield/backend/internal/battle/service"
	"github.com/twh09/autonomous_battlefield/backend/internal/httpapi"
)

// errorCase answers any of its errors with one status and message. A non-empty code
// overrides the status's default code.
type errorCase struct {
	errs    []error
	status  int
	code    apierror.ErrorCode
	message string
}

func errorFor(status int, message string, errs ...error) errorCase {
	return errorCase{errs: errs, status: status, message: message}
}

var (
	forbiddenError = errorFor(http.StatusForbidden, permissionDeniedMessage, authn.ErrForbidden)
	limitedError   = errorFor(http.StatusTooManyRequests, battleAllowanceMessage, battleservice.ErrLimited)
	notFoundError  = errorFor(http.StatusNotFound, battleNotFoundMessage, battleservice.ErrBattleNotFound)
	notActiveError = errorFor(http.StatusConflict, "battle is not active", battleservice.ErrBattleNotActive)
	// replayUnavailableError covers active battles and battles without saved checkpoints.
	replayUnavailableError = errorFor(http.StatusConflict, "battle replay unavailable", battleservice.ErrReplayUnavailable)
)

// invalidError answers ErrInvalidBattle, plus any extra errors, with a route-specific
// 400 message.
func invalidError(message string, extra ...error) errorCase {
	return errorFor(http.StatusBadRequest, message, append([]error{battleservice.ErrInvalidBattle}, extra...)...)
}

// failure describes the logged, generic 500 for an unexpected error. An empty event
// writes the 500 without logging.
type failure struct {
	event   string
	message string
	attrs   []any
}

// writeError answers err with the first matching case, or logs it and writes a generic
// 500 so internal details never reach the client. It reports whether err was non-nil.
func writeError(w http.ResponseWriter, r *http.Request, err error, cases []errorCase, unexpected failure) bool {
	if err == nil {
		return false
	}
	for _, c := range cases {
		for _, target := range c.errs {
			if errors.Is(err, target) {
				if c.code != "" {
					apierror.WriteCode(w, r, c.status, c.code, c.message)
				} else {
					apierror.Write(w, r, c.status, c.message)
				}
				return true
			}
		}
	}
	if unexpected.event != "" {
		attrs := append([]any{"request_id", httpapi.RequestID(r.Context())}, unexpected.attrs...)
		slog.ErrorContext(r.Context(), unexpected.event, append(attrs, "error", err)...)
	}
	apierror.Write(w, r, http.StatusInternalServerError, unexpected.message)
	return true
}
