package constants

// Unified business error codes used by the {code,message,data} response envelope.
const (
	CodeOK            = 0
	CodeBadRequest    = 40000
	CodeUnauthorized  = 40100
	CodeForbidden     = 40300
	CodeNotFound      = 40400
	CodeConflict      = 40900
	CodeRateLimited   = 42900
	CodeValidation    = 42200
	CodeHandoverMismatch = 40910
	CodeHandoverExpired  = 40911
	CodeHandoverUsed     = 40912
	CodeHandoverNoCode   = 40913
	CodeInternalError = 50000
)
