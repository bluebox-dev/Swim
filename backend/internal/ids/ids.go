// Package ids makes prefixed ULIDs (spec 10: h_, img_, job_ …).
package ids

import (
	"strings"

	"github.com/oklog/ulid/v2"
)

func New(prefix string) string {
	return prefix + strings.ToLower(ulid.Make().String())
}
