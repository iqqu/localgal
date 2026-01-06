package server

import (
	"net/http"
	"net/url"
	"slices"
	"time"
)

const (
	SortFetched  string = "fetched"
	SortUploaded string = "uploaded"

	SortItems string = "items"
	SortBytes string = "bytes"

	SortRank    string = "rank"
	SortDefault string = ""
)

var GallerySorts = []string{SortFetched, SortUploaded, SortBytes, SortItems}
var FileSorts = []string{SortFetched, SortUploaded, SortBytes}
var GallerySearchSorts = []string{SortRank, SortFetched, SortUploaded, SortBytes, SortItems}
var FileSearchSorts = []string{SortRank, SortFetched, SortUploaded, SortBytes}

func getSort(w http.ResponseWriter, r *http.Request, cookieName string, validSorts []string) string {
	var defaultSortValue string
	defaultSort, err := r.Cookie(cookieName)
	if err == nil && slices.Contains(validSorts, defaultSort.Value) {
		defaultSortValue = defaultSort.Value
	} else {
		defaultSortValue = ""
	}

	sortQs := r.URL.Query().Get("sort")
	if !slices.Contains(validSorts, sortQs) {
		sortQs = ""
	}

	sortQsWasValid := sortQs != ""
	newDefaultSortValue := sortQs != defaultSortValue
	if sortQsWasValid && newDefaultSortValue {
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    sortQs,
			Path:     "/",
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int((6 * time.Hour).Seconds()),
		})
	}
	if sortQs == "" {
		return defaultSortValue
	}
	return sortQs
}

func getSortGalleries(w http.ResponseWriter, r *http.Request) string {
	return getSort(w, r, "defaultSortGalleries", GallerySorts)
}

func getSortFiles(w http.ResponseWriter, r *http.Request) string {
	return getSort(w, r, "defaultSortFiles", FileSorts)
}

func getSortSearchGalleries(w http.ResponseWriter, r *http.Request) string {
	return getSort(w, r, "defaultSortSearchGalleries", GallerySearchSorts)
}

func getSortSearchFiles(w http.ResponseWriter, r *http.Request) string {
	return getSort(w, r, "defaultSortSearchFiles", FileSearchSorts)
}

func getUrlSort(u *url.URL, validSorts []string) string {
	sortQs := u.Query().Get("sort")
	if !slices.Contains(validSorts, sortQs) {
		sortQs = ""
	}
	return sortQs
}

func getUrlSortGalleries(u *url.URL) string {
	return getUrlSort(u, GallerySorts)
}

func getUrlSortFiles(u *url.URL) string {
	return getUrlSort(u, FileSorts)
}

func getUrlSortSearchGalleries(u *url.URL) string {
	return getUrlSort(u, GallerySearchSorts)
}

func getUrlSortSearchFiles(u *url.URL) string {
	return getUrlSort(u, FileSearchSorts)
}
