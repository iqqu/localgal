package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golocalgal/internal/types"
)

// ratingFilterSQL returns a SQL clause and bind args for the rating filter.
// column is the SQL column name (e.g. "a.local_rating" or "rf.local_rating").
// Never pass user input into column.
// Returns ("", nil) when no filter is active.
func ratingFilterSQL(column string, rf types.RatingFilter) (string, []any) {
	// Only unrated = always just IS NULL, ignore min/max
	if rf.Unrated == types.UnratedOnly {
		return fmt.Sprintf("AND %s IS NULL", column), nil
	}

	var rangeClause string
	var args []any
	if rf.Min > 0 && rf.Max > 0 {
		rangeClause = fmt.Sprintf("%s >= ? AND %s <= ?", column, column)
		args = []any{rf.Min, rf.Max}
	} else if rf.Min > 0 {
		rangeClause = fmt.Sprintf("%s >= ?", column)
		args = []any{rf.Min}
	} else if rf.Max > 0 {
		rangeClause = fmt.Sprintf("%s <= ?", column)
		args = []any{rf.Max}
	}

	if rangeClause == "" {
		return "", nil
	}

	// Exclude unrated = no explicit OR NULL
	if rf.Unrated == types.UnratedExclude {
		return "AND " + rangeClause, args
	}

	// Include unrated or default = range OR NULL
	return fmt.Sprintf("AND (%s OR %s IS NULL)", rangeClause, column), args
}

func parseRatingValue(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 || v > 5 {
		return 0
	}
	return v
}

func parseUnratedValue(s string) string {
	switch s {
	case types.UnratedExclude, types.UnratedOnly:
		return s
	default:
		return ""
	}
}

func getRatingFilterWithPrefix(w http.ResponseWriter, r *http.Request, minParam, maxParam, unratedParam, minCookie, maxCookie, unratedCookie string) types.RatingFilter {
	rf := types.RatingFilter{}

	// Read cookie defaults
	if c, err := r.Cookie(minCookie); err == nil {
		rf.Min = parseRatingValue(c.Value)
	}
	if c, err := r.Cookie(maxCookie); err == nil {
		rf.Max = parseRatingValue(c.Value)
	}
	if c, err := r.Cookie(unratedCookie); err == nil {
		rf.Unrated = parseUnratedValue(c.Value)
	}

	query := r.URL.Query()

	// Process min
	if query.Has(minParam) {
		qMin := parseRatingValue(query.Get(minParam))
		if qMin > 0 {
			if qMin != rf.Min {
				http.SetCookie(w, &http.Cookie{
					Name:     minCookie,
					Value:    strconv.Itoa(qMin),
					Path:     "/",
					SameSite: http.SameSiteStrictMode,
					MaxAge:   int((6 * time.Hour).Seconds()),
				})
			}
			rf.Min = qMin
		} else {
			// Clear invalid value
			http.SetCookie(w, &http.Cookie{
				Name:     minCookie,
				Path:     "/",
				SameSite: http.SameSiteStrictMode,
				MaxAge:   -1,
			})
			rf.Min = 0
		}
	}

	// Process max
	if query.Has(maxParam) {
		qMax := parseRatingValue(query.Get(maxParam))
		if qMax > 0 {
			if qMax != rf.Max {
				http.SetCookie(w, &http.Cookie{
					Name:     maxCookie,
					Value:    strconv.Itoa(qMax),
					Path:     "/",
					SameSite: http.SameSiteStrictMode,
					MaxAge:   int((6 * time.Hour).Seconds()),
				})
			}
			rf.Max = qMax
		} else {
			http.SetCookie(w, &http.Cookie{
				Name:     maxCookie,
				Path:     "/",
				SameSite: http.SameSiteStrictMode,
				MaxAge:   -1,
			})
			rf.Max = 0
		}
	}

	// Process unrated
	if query.Has(unratedParam) {
		qUnrated := parseUnratedValue(query.Get(unratedParam))
		if qUnrated != "" {
			if qUnrated != rf.Unrated {
				http.SetCookie(w, &http.Cookie{
					Name:     unratedCookie,
					Value:    qUnrated,
					Path:     "/",
					SameSite: http.SameSiteStrictMode,
					MaxAge:   int((6 * time.Hour).Seconds()),
				})
			}
			rf.Unrated = qUnrated
		} else {
			http.SetCookie(w, &http.Cookie{
				Name:     unratedCookie,
				Path:     "/",
				SameSite: http.SameSiteStrictMode,
				MaxAge:   -1,
			})
			rf.Unrated = ""
		}
	}

	// Swap if min > max and both are set
	if rf.Min > 0 && rf.Max > 0 && rf.Min > rf.Max {
		rf.Min, rf.Max = rf.Max, rf.Min
	}

	return rf
}

func getGalleryRatingFilter(w http.ResponseWriter, r *http.Request) types.RatingFilter {
	return getRatingFilterWithPrefix(w, r, "gal_rating_min", "gal_rating_max", "gal_unrated", "defaultGalRatingMin", "defaultGalRatingMax", "defaultGalUnrated")
}

func getFileRatingFilter(w http.ResponseWriter, r *http.Request) types.RatingFilter {
	return getRatingFilterWithPrefix(w, r, "file_rating_min", "file_rating_max", "file_unrated", "defaultFileRatingMin", "defaultFileRatingMax", "defaultFileUnrated")
}

func getUrlRatingFilterWithPrefix(u *url.URL, minParam, maxParam, unratedParam string) types.RatingFilter {
	query := u.Query()
	rf := types.RatingFilter{
		Min:     parseRatingValue(query.Get(minParam)),
		Max:     parseRatingValue(query.Get(maxParam)),
		Unrated: parseUnratedValue(query.Get(unratedParam)),
	}
	if rf.Min > 0 && rf.Max > 0 && rf.Min > rf.Max {
		rf.Min, rf.Max = rf.Max, rf.Min
	}
	return rf
}

func getUrlGalleryRatingFilter(u *url.URL) types.RatingFilter {
	return getUrlRatingFilterWithPrefix(u, "gal_rating_min", "gal_rating_max", "gal_unrated")
}

func getUrlFileRatingFilter(u *url.URL) types.RatingFilter {
	return getUrlRatingFilterWithPrefix(u, "file_rating_min", "file_rating_max", "file_unrated")
}
