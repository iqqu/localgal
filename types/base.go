package types

import (
	"database/sql"
	"encoding/json"
	"time"
)

type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

type Album struct {
	AlbumId     int64         `json:"albumId,omitempty,omitzero"`
	RipperId    int64         `json:"-"`
	RipperName  string        `json:"ripperName,omitempty,omitzero"`
	RipperHost  string        `json:"ripperHost,omitempty,omitzero"`
	Gid         string        `json:"gid,omitempty,omitzero"`
	Uploader    SqlJsonString `json:"uploader,omitempty,omitzero"`
	Title       SqlJsonString `json:"title,omitempty,omitzero"`
	Description SqlJsonString `json:"description,omitempty,omitzero"`
	CreatedTs   SqlJsonInt64  `json:"createdTs,omitempty,omitzero"`
	ModifiedTs  SqlJsonInt64  `json:"modifiedTs,omitempty,omitzero"`
	Hidden      bool          `json:"hidden,omitempty,omitzero"`
	Removed     bool          `json:"removed,omitempty,omitzero"`
	LocalRating SqlJsonInt64  `json:"localRating,omitempty,omitzero"`
	LastFetchTs SqlJsonInt64  `json:"lastFetchTs,omitempty,omitzero"`
	InsertedTs  int64         `json:"insertedTs,omitempty,omitzero"`
	FileCount   int           `json:"fileCount,omitempty,omitzero"`
	HrefPage    string        `json:"hrefPage,omitempty,omitzero"`
	Thumb       File          `json:"thumb,omitempty,omitzero"` // representative file for album thumbnail tile
}

type File struct {
	FileId      int64         `json:"fileId,omitempty,omitzero"`
	RipperName  string        `json:"ripperName,omitempty,omitzero"`
	RipperHost  string        `json:"ripperHost,omitempty,omitzero"`
	Urlid       SqlJsonString `json:"urlid,omitempty,omitzero"`
	Filename    SqlJsonString `json:"filename,omitempty,omitzero"`
	MimeType    SqlJsonString `json:"mimeType,omitempty,omitzero"`
	Title       SqlJsonString `json:"title,omitempty,omitzero"`
	Description SqlJsonString `json:"description,omitempty,omitzero"`
	UploadedTs  SqlJsonInt64  `json:"uploadedTs,omitempty,omitzero"`
	Uploader    SqlJsonString `json:"uploader,omitempty,omitzero"`
	Hidden      bool          `json:"hidden,omitempty,omitzero"`
	Removed     bool          `json:"removed,omitempty,omitzero"`
	Bytes       SqlJsonInt64  `json:"bytes,omitempty,omitzero"`
	LocalRating SqlJsonInt64  `json:"localRating,omitempty,omitzero"`
	InsertedTs  int64         `json:"insertedTs,omitempty,omitzero"`
	HrefPage    string        `json:"hrefPage,omitempty,omitzero"`
	HrefMedia   string        `json:"hrefMedia,omitempty,omitzero"`
	AlbumId     int64         `json:"-"`
}

type Tag struct {
	TagId   int64  `json:"-"`
	Name    string `json:"name,omitempty,omitzero"`
	IsLocal bool   `json:"isLocal,omitempty,omitzero"`
	Count   int    `json:"count,omitempty,omitzero"` // optional usage count for tag listings
}

// SqlJsonString marshalls to the value of the string or null
type SqlJsonString struct {
	sql.NullString
}

func (v SqlJsonString) IsZero() bool {
	return !v.Valid
}
func (v SqlJsonString) MarshalJSON() ([]byte, error) {
	if !v.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(v.String)
}

// SqlJsonInt64 marshalls to the value of the int64 or null
type SqlJsonInt64 struct {
	sql.NullInt64
}

func (v SqlJsonInt64) IsZero() bool {
	return !v.Valid
}
func (v SqlJsonInt64) MarshalJSON() ([]byte, error) {
	if !v.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(v.Int64)
}

type Perf struct {
	SQLCount int           `json:"sqlCount"`
	SQLTime  time.Duration `json:"sqlTime"`
	PageTime time.Duration `json:"pageTime"`
	Start    time.Time     `json:"-"`
}

func (p Perf) MarshalJSON() ([]byte, error) {
	if !p.Start.IsZero() {
		p.PageTime = time.Since(p.Start)
	}
	return json.Marshal(PerfJSON{
		SQLCount: p.SQLCount,
		SQLTime:  p.SQLTime,
		PageTime: marshalElapsed{Start: &p.Start},
		Start:    p.Start,
	})
}

// PerfJSON is used for populating PageTime when serializing Perf in JSON
type PerfJSON struct {
	SQLCount int            `json:"sqlCount"`
	SQLTime  time.Duration  `json:"sqlTime"`
	PageTime marshalElapsed `json:"pageTime"`
	Start    time.Time      `json:"-"`
}

type marshalElapsed struct {
	Start *time.Time
}

func (m marshalElapsed) MarshalJSON() ([]byte, error) {
	if m.Start == nil || m.Start.IsZero() {
		return json.Marshal(time.Duration(0))
	}
	elapsed := time.Since(*m.Start)
	return json.Marshal(elapsed)
}
