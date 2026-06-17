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
	if rf.Min > 0 && rf.Max > 0 {
		return fmt.Sprintf("AND %s >= ? AND %s <= ?", column, column), []any{rf.Min, rf.Max}
	} else if rf.Min > 0 {
		return fmt.Sprintf("AND %s >= ?", column), []any{rf.Min}
	} else if rf.Max > 0 {
		return fmt.Sprintf("AND %s <= ?", column), []any{rf.Max}
	}
	return "", nil
}

func parseRatingValue(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 || v > 5 {
		return 0
	}
	return v
}

func getRatingFilter(w http.ResponseWriter, r *http.Request) types.RatingFilter {
	rf := types.RatingFilter{}

	// Read cookie defaults
	if c, err := r.Cookie("defaultRatingMin"); err == nil {
		rf.Min = parseRatingValue(c.Value)
	}
	if c, err := r.Cookie("defaultRatingMax"); err == nil {
		rf.Max = parseRatingValue(c.Value)
	}

	query := r.URL.Query()

	// Process rating_min
	if query.Has("rating_min") {
		qMin := parseRatingValue(query.Get("rating_min"))
		if qMin > 0 {
			if qMin != rf.Min {
				http.SetCookie(w, &http.Cookie{
					Name:     "defaultRatingMin",
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
				Name:     "defaultRatingMin",
				Path:     "/",
				SameSite: http.SameSiteStrictMode,
				MaxAge:   -1,
			})
			rf.Min = 0
		}
	}

	// Process rating_max
	if query.Has("rating_max") {
		qMax := parseRatingValue(query.Get("rating_max"))
		if qMax > 0 {
			if qMax != rf.Max {
				http.SetCookie(w, &http.Cookie{
					Name:     "defaultRatingMax",
					Value:    strconv.Itoa(qMax),
					Path:     "/",
					SameSite: http.SameSiteStrictMode,
					MaxAge:   int((6 * time.Hour).Seconds()),
				})
			}
			rf.Max = qMax
		} else {
			http.SetCookie(w, &http.Cookie{
				Name:     "defaultRatingMax",
				Path:     "/",
				SameSite: http.SameSiteStrictMode,
				MaxAge:   -1,
			})
			rf.Max = 0
		}
	}

	// Swap if min > max and both are set
	if rf.Min > 0 && rf.Max > 0 && rf.Min > rf.Max {
		rf.Min, rf.Max = rf.Max, rf.Min
	}

	return rf
}

func getUrlRatingFilter(u *url.URL) types.RatingFilter {
	query := u.Query()
	rf := types.RatingFilter{
		Min: parseRatingValue(query.Get("rating_min")),
		Max: parseRatingValue(query.Get("rating_max")),
	}
	if rf.Min > 0 && rf.Max > 0 && rf.Min > rf.Max {
		rf.Min, rf.Max = rf.Max, rf.Min
	}
	return rf
}
