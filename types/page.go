package types

type BasePage struct {
	Perf *Perf `json:"perf"`
}

type BrowsePage struct {
	Albums   []Album `json:"albums"`
	Page     int     `json:"page"`
	PageSize int     `json:"pageSize"`
	Total    int     `json:"total"`
	HasPrev  bool    `json:"hasPrev"`
	HasNext  bool    `json:"hasNext"`
	Sort     string  `json:"sort,omitempty,omitzero"`
	//Perf     Perf    `json:"perf"`
	BasePage
}

type GalleryPage struct {
	Album      Album  `json:"album"`
	Files      []File `json:"files"`
	Page       int    `json:"page"`
	PageSize   int    `json:"pageSize"`
	Total      int    `json:"total"`
	HasPrev    bool   `json:"hasPrev"`
	HasNext    bool   `json:"hasNext"`
	AlbumTags  []Tag  `json:"albumTags"`
	FileTags   []Tag  `json:"fileTags"`
	AlbumBytes int64  `json:"albumBytes"`
	Sort       string `json:"sort,omitempty,omitzero"`
	//Perf      Perf   `json:"perf"`
	BasePage
}

type SearchPage struct {
	Query       string  `json:"query"`
	Albums      []Album `json:"albums"`
	AlbumsTotal int     `json:"albumsTotal"`
	Files       []File  `json:"files"`
	FilesTotal  int     `json:"filesTotal"`
	Tags        []Tag   `json:"tags"`
	TagsTotal   int     `json:"tagsTotal"`
	HasNext     bool    `json:"hasNext"`
	HasPrev     bool    `json:"hasPrev"`
	Page        int     `json:"page"`
	PageSize    int     `json:"pageSize"`
	Sort        string  `json:"sort,omitempty,omitzero"`
	//Perf      Perf   `json:"perf"`
	BasePage
}

type SearchErrorPage struct {
	Query   string `json:"query"`
	Message string `json:"message"`
	//Perf      Perf   `json:"perf"`
	BasePage
}

type FilePage struct {
	File         File    `json:"file"`
	Prev         []File  `json:"prev"`
	Next         []File  `json:"next"`
	FileTags     []Tag   `json:"fileTags"`
	AsyncAlbums  bool    `json:"-"`
	Albums       []Album `json:"albums"`
	CurrentAlbum Album   `json:"currentAlbum"` // album when viewing within an album; nil for standalone
	ShowPrevNext bool    `json:"showPrevNext"` // whether to show prev/next rail
	//Perf         Perf    `json:"perf"`
	BasePage
}

type TagsPage struct {
	ImageTags []Tag `json:"imageTags"`
	AlbumTags []Tag `json:"albumTags"`
	//Perf      Perf  `json:"perf"`
	BasePage
}

type TagDetailPage struct {
	Tag      Tag     `json:"tag"`
	Albums   []Album `json:"albums"`
	Files    []File  `json:"files"`
	Page     int     `json:"page"`
	PageSize int     `json:"pageSize"`
	Total    int     `json:"total"`
	HasPrev  bool    `json:"hasPrev"`
	HasNext  bool    `json:"hasNext"`
	//Perf     Perf    `json:"perf"`
	BasePage
}

type ErrorPage struct {
	StatusText string `json:"statusText"`
	Message    string `json:"message"`
	//Perf       Perf   `json:"perf"`
	BasePage
}
