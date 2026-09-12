package crawler

import "errors"

var (
	ErrMovieNotFound  = errors.New("movie not found")
	ErrMovieDuplicate = errors.New("movie duplicate")
	ErrSiteBlocked    = errors.New("site blocked")
	ErrSitePermission = errors.New("site permission")
	ErrCredential     = errors.New("credential error")
	ErrWebsite        = errors.New("website error")
	ErrOther          = errors.New("crawler error")
)

func IsTerminal(err error) bool {
	return errors.Is(err, ErrMovieNotFound) ||
		errors.Is(err, ErrMovieDuplicate) ||
		errors.Is(err, ErrSiteBlocked) ||
		errors.Is(err, ErrSitePermission) ||
		errors.Is(err, ErrCredential)
}
