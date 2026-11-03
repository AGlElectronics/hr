// Package service holds the HR business logic. Handlers in internal/http
// are thin: they decode JSON, call a service method, and render the result.
package service

import "github.com/AGlElectronics/hr/backend/internal/httperror"

// Shortcuts so services can call e.g. service.NotFound("person") without
// importing httperror everywhere.
var (
	ErrNotFound     = httperror.ErrNotFound
	ErrForbidden    = httperror.ErrForbidden
	ErrUnauthorized = httperror.ErrUnauthorized
)
