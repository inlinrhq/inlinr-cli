package heartbeat

import (
	"path"
	"strings"
)

// HiddenName is what a file is sent as under `hide_file_names`: its
// extension kept, so the server still tells the language, its name and
// folders never sent.
const HiddenName = "HIDDEN"

// Hide takes out what the person chose not to send: the file's path
// (`hide_file_names`) and the branch (`hide_branch_names`). Only a file is
// renamed; an app's or a domain's name is not a path.
func (h *Heartbeat) Hide(files, branches bool) {
	if files && (h.Type == "" || h.Type == "file") {
		h.Entity = HiddenName + extension(h.Entity)
	}
	if branches {
		h.Branch = nil
	}
}

// extension of a path written with either separator: ".ts" for
// "src\\a.ts", "" for "Makefile" or ".env".
func extension(p string) string {
	base := path.Base(strings.ReplaceAll(p, "\\", "/"))
	ext := path.Ext(base)
	if ext == base || len(ext) > 16 {
		return ""
	}
	return ext
}
