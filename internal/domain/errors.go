package domain

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound      = errors.New("tracking not found")
	ErrInvalid       = errors.New("invalid tracking")
	ErrConflict      = errors.New("tracking conflict")
	ErrNotConfigured = errors.New("tracking not configured")
)

// Error codes. -1801xxxx are 17TRACK v2 codes, returned with 17TRACK's meaning (and
// passed through verbatim when 17TRACK itself rejects an item). -2801xxxx are
// wrapper-only conditions 17TRACK has no code for.
const (
	CodeOK = 0

	CodeInvalidToken      = -18010002 // Invalid access security key.
	CodeInternal          = -18010003 // Internal service error, please try again later.
	CodeFieldRequired     = -18010010 // The data of field {0} is required.
	CodeFieldInvalid      = -18010011 // The value of field {0} is invalid.
	CodeFormatInvalid     = -18010012 // The format of '{0}' is invalid.
	CodeDataInvalid       = -18010013 // The submitted data is invalid.
	CodeTooManyNumbers    = -18010014 // The submitted tracking number request parameter exceeds 40 limit.
	CodeAlreadyRegistered = -18019901 // The tracking number {0} has been registered, don't need to repeat registration.
	CodeNotRegistered     = -18019902 // The tracking number {0} does not register, please register first.
	CodeCarrierUndetected = -18019903 // Carrier cannot be detected.
	CodeStopNotAllowed    = -18019906 // Only tracking numbers that are being tracked can be stopped.

	CodeStateConflict      = -28019001 // Subscription state does not allow the operation.
	CodeArchiveUnavailable = -28019002 // Archived events unavailable (cold storage not configured / unreachable).
)

// MaxBatch is 17TRACK's per-request item limit, enforced the same way here.
const MaxBatch = 40

// ErrorBody is the 17TRACK error object ({code, message}).
type ErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CodedError is an error carrying a 17TRACK-style code for API responses.
type CodedError struct {
	ErrorBody
	// Kind is one of the Err* sentinels for errors.Is.
	Kind error
}

func (e *CodedError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }
func (e *CodedError) Unwrap() error { return e.Kind }

// Errorf builds a CodedError; kind is the matching Err* sentinel (may be nil).
func Errorf(kind error, code int, format string, args ...any) error {
	return &CodedError{ErrorBody: ErrorBody{Code: code, Message: fmt.Sprintf(format, args...)}, Kind: kind}
}

// Field-level helpers with 17TRACK's wording.
func ErrRequired(field string) error {
	return Errorf(ErrInvalid, CodeFieldRequired, "The data of field %s is required.", field)
}

func ErrInvalidValue(field string) error {
	return Errorf(ErrInvalid, CodeFieldInvalid, "The value of field %s is invalid.", field)
}

func ErrTooMany() error {
	return Errorf(ErrInvalid, CodeTooManyNumbers, "The submitted tracking number request parameter exceeds %d limit.", MaxBatch)
}

func ErrNotRegistered(number string) error {
	return Errorf(ErrNotFound, CodeNotRegistered, "The tracking number %s does not register, please register first.", number)
}

// Body maps any error to its 17TRACK-style body.
func Body(err error) ErrorBody {
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.ErrorBody
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrorBody{CodeNotRegistered, err.Error()}
	case errors.Is(err, ErrInvalid):
		return ErrorBody{CodeDataInvalid, err.Error()}
	case errors.Is(err, ErrConflict):
		return ErrorBody{CodeStateConflict, err.Error()}
	default:
		return ErrorBody{CodeInternal, "Internal service error, please try again later."}
	}
}
