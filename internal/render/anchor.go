package render

import (
	"fmt"
	"strings"
	"unicode"
)

// Anchor converts a heading to its GitHub anchor: trimmed, lower-case,
// every rune that is not a letter, digit, space, "-" or "_" dropped,
// spaces to "-".
func Anchor(heading string) string {
	heading = strings.TrimSpace(heading)
	var b strings.Builder
	b.Grow(len(heading))
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Anchorer assigns GitHub anchors over one document in final order: the
// first occurrence of a heading keeps the plain anchor, later ones become
// x-1, x-2, … GitHub numbers over every heading, not only fact headings,
// so the whole document must be walked through one Anchorer.
type Anchorer struct {
	seen map[string]int
}

func NewAnchorer() *Anchorer {
	return &Anchorer{seen: map[string]int{}}
}

func (a *Anchorer) Next(heading string) string {
	slug := Anchor(heading)
	a.seen[slug]++
	if a.seen[slug] == 1 {
		return slug
	}
	return fmt.Sprintf("%s-%d", slug, a.seen[slug]-1)
}
