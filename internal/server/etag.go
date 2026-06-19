package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
)

// computeETag generates a deterministic ETag based on relevant query parameters and the URL path.
func computeETag(r *http.Request) string {
	query := r.URL.Query()

	// List of parameters that affect page content
	relevantParams := []string{
		"page", "size", "sort",
		"gal_rating_min", "gal_rating_max", "gal_unrated",
		"file_rating_min", "file_rating_max", "file_unrated",
		"file_type", "q",
	}

	// Map of parameters to their corresponding cookie names
	paramToCookie := map[string]string{
		"gal_rating_min":  "defaultGalRatingMin",
		"gal_rating_max":  "defaultGalRatingMax",
		"gal_unrated":     "defaultGalUnrated",
		"file_rating_min": "defaultFileRatingMin",
		"file_rating_max": "defaultFileRatingMax",
		"file_unrated":    "defaultFileUnrated",
		"file_type":       "defaultFileType",
	}

	// Sort keys for deterministic output
	sort.Strings(relevantParams)

	h := sha256.New()
	// Include path to distinguish between different resources
	h.Write([]byte(r.URL.Path))

	for _, p := range relevantParams {
		if query.Has(p) {
			h.Write([]byte(p))
			h.Write([]byte(query.Get(p)))
		} else if cookieName, ok := paramToCookie[p]; ok {
			if c, err := r.Cookie(cookieName); err == nil {
				h.Write([]byte(p))
				h.Write([]byte(c.Value))
			}
		}
	}

	// Bust cache if cacheBust cookie is set
	if c, err := r.Cookie("cacheBust"); err == nil {
		h.Write([]byte("cacheBust"))
		h.Write([]byte(c.Value))
	}

	return hex.EncodeToString(h.Sum(nil))
}
