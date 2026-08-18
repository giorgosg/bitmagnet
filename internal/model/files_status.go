package model

// FilesStatus represents what we know about the files in a Torrent
// ENUM(no_info, single, multi, over_threshold)
type FilesStatus string

// HasStoredFileList reports whether a torrent with this status is expected to
// have a file list available to classifier rules.
func (x FilesStatus) HasStoredFileList() bool {
	return x != FilesStatusNoInfo && x != FilesStatusOverThreshold
}
