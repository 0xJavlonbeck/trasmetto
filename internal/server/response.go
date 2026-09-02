package server

import (
	"fmt"
	"net/url"
)

func contentDispositionAttachment(filename string) string {
	escaped := url.PathEscape(filename)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", filename, escaped)
}
