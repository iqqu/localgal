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

	// TODO materialize slow aggregate calculations
	//SortItems string = "items"
	SortBytes string = "bytes"

	SortRank    string = "rank"
	SortDefault string = ""
)

var GallerySorts = []string{SortFetched, SortUploaded}
var FileSorts = []string{SortFetched, SortUploaded, SortBytes}
var GallerySearchSorts = []string{SortRank, SortFetched, SortUploaded}
var FileSearchSorts = []string{SortRank, SortFetched, SortUploaded, SortBytes}

func getSortGalleries(w http.ResponseWriter, r *http.Request) string {
	var defaultSortValue string
	defaultSort, err := r.Cookie("defaultSortGalleries")
	if err == nil && slices.Contains(GallerySorts, defaultSort.Value) {
		defaultSortValue = defaultSort.Value
	} else {
		defaultSortValue = ""
	}

	sortQs := r.URL.Query().Get("sort")
	if !slices.Contains(GallerySorts, sortQs) {
		sortQs = ""
	}

	sortQsWasValid := sortQs != ""
	newDefaultSortValue := sortQs != defaultSortValue
	if sortQsWasValid && newDefaultSortValue {
		http.SetCookie(w, &http.Cookie{
			Name:     "defaultSortGalleries",
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

func getSortFiles(w http.ResponseWriter, r *http.Request) string {
	var defaultSortValue string
	defaultSort, err := r.Cookie("defaultSortFiles")
	if err == nil && slices.Contains(FileSorts, defaultSort.Value) {
		defaultSortValue = defaultSort.Value
	} else {
		defaultSortValue = ""
	}

	sortQs := r.URL.Query().Get("sort")
	if !slices.Contains(FileSorts, sortQs) {
		sortQs = ""
	}

	sortQsWasValid := sortQs != ""
	newDefaultSortValue := sortQs != defaultSortValue
	if sortQsWasValid && newDefaultSortValue {
		http.SetCookie(w, &http.Cookie{
			Name:     "defaultSortFiles",
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

func getSortSearchGalleries(w http.ResponseWriter, r *http.Request) string {
	var defaultSortValue string
	defaultSort, err := r.Cookie("defaultSortSearchGalleries")
	if err == nil && slices.Contains(GallerySearchSorts, defaultSort.Value) {
		defaultSortValue = defaultSort.Value
	} else {
		defaultSortValue = ""
	}

	sortQs := r.URL.Query().Get("sort")
	if !slices.Contains(GallerySearchSorts, sortQs) {
		sortQs = ""
	}

	sortQsWasValid := sortQs != ""
	newDefaultSortValue := sortQs != defaultSortValue
	if sortQsWasValid && newDefaultSortValue {
		http.SetCookie(w, &http.Cookie{
			Name:     "defaultSortSearchGalleries",
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

func getSortSearchFiles(w http.ResponseWriter, r *http.Request) string {
	var defaultSortValue string
	defaultSort, err := r.Cookie("defaultSortSearchFiles")
	if err == nil && slices.Contains(FileSearchSorts, defaultSort.Value) {
		defaultSortValue = defaultSort.Value
	} else {
		defaultSortValue = ""
	}

	sortQs := r.URL.Query().Get("sort")
	if !slices.Contains(FileSearchSorts, sortQs) {
		sortQs = ""
	}

	sortQsWasValid := sortQs != ""
	newDefaultSortValue := sortQs != defaultSortValue
	if sortQsWasValid && newDefaultSortValue {
		http.SetCookie(w, &http.Cookie{
			Name:     "defaultSortSearchFiles",
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

func getUrlSortGalleries(url *url.URL) string {
	sortQs := url.Query().Get("sort")
	if !slices.Contains(GallerySorts, sortQs) {
		sortQs = ""
	}
	return sortQs
}

func getUrlSortFiles(url *url.URL) string {
	sortQs := url.Query().Get("sort")
	if !slices.Contains(FileSorts, sortQs) {
		sortQs = ""
	}
	return sortQs
}

func getUrlSortSearchGalleries(url *url.URL) string {
	sortQs := url.Query().Get("sort")
	if !slices.Contains(GallerySearchSorts, sortQs) {
		sortQs = ""
	}
	return sortQs
}

func getUrlSortSearchFiles(url *url.URL) string {
	sortQs := url.Query().Get("sort")
	if !slices.Contains(FileSearchSorts, sortQs) {
		sortQs = ""
	}
	return sortQs
}
