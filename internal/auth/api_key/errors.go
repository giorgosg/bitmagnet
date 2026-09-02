package api_key

import "errors"

var (
	Err       = errors.New("api key")
	ErrCreate = errors.New("create failed")
	// ErrPermissionInvalid is returned for a requested object action that is not
	// one of the registered ones - a typo, or a glob pattern.
	ErrPermissionInvalid = errors.New("invalid api key permission")
	ErrAuth              = errors.New("auth failed")
	ErrDecode            = errors.New("decode failed")
	ErrMismatch          = errors.New("mismatch")
	ErrLength            = errors.New("wrong length")
	ErrList              = errors.New("list failed")
	ErrDelete            = errors.New("delete failed")
	ErrNotFound          = errors.New("not found")
	ErrExpired           = errors.New("expired")
	ErrDisabled          = errors.New("account is disabled")
	ErrRepository        = errors.New("repository")
)
