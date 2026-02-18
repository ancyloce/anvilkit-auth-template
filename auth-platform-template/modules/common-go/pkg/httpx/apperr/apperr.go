package apperr

import (
	"errors"
	"net/http"

	"auth-platform-template/modules/common-go/pkg/httpx/errcode"
	"github.com/jackc/pgx/v5/pgconn"
)

type AppError struct {
	HTTPStatus int
	Code       int
	Message    string
	Data       map[string]any
	Err        error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

func (e *AppError) Unwrap() error { return e.Err }

func New(httpStatus, code int, msg string, err error) *AppError {
	return &AppError{HTTPStatus: httpStatus, Code: code, Message: msg, Err: err}
}

func (e *AppError) WithData(data map[string]any) *AppError {
	e.Data = data
	return e
}

func BadRequest(err error) *AppError {
	return New(http.StatusBadRequest, errcode.BadRequest, "bad_request", err)
}
func Unauthorized(err error) *AppError {
	return New(http.StatusUnauthorized, errcode.Unauthorized, "unauthorized", err)
}
func Forbidden(err error) *AppError {
	return New(http.StatusForbidden, errcode.Forbidden, "forbidden", err)
}
func NotFound(err error) *AppError {
	return New(http.StatusNotFound, errcode.NotFound, "not_found", err)
}
func Conflict(err error) *AppError {
	return New(http.StatusConflict, errcode.Conflict, "conflict", err)
}
func RateLimited(err error) *AppError {
	return New(http.StatusTooManyRequests, errcode.RateLimited, "rate_limited", err)
}

func Normalize(err error) *AppError {
	if err == nil {
		return New(http.StatusInternalServerError, errcode.InternalError, "internal_error", errors.New("nil_error"))
	}
	var ae *AppError
	if errors.As(err, &ae) {
		return ae
	}
	var pge *pgconn.PgError
	if errors.As(err, &pge) {
		if pge.Code == "23505" {
			return Conflict(err).WithData(map[string]any{"reason": "unique_violation"})
		}
		return New(http.StatusInternalServerError, errcode.DBError, "db_error", err)
	}
	return New(http.StatusInternalServerError, errcode.InternalError, "internal_error", err)
}
