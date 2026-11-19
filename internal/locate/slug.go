// Package locate finds the Claude Code memory directories that belong to
// a project directory: the project's own slug, its subdirectories and its
// .claude/worktrees/*, including worktrees that were already deleted.
package locate

import "strings"

// Encode builds the Claude Code project slug for an absolute path: every
// rune that is not an ASCII letter or digit becomes one "-", except runes
// outside the Unicode BMP, which encode as "--" — Claude Code builds
// slugs in JavaScript, where such a rune is two UTF-16 units.
func Encode(absPath string) string {
	var b strings.Builder
	b.Grow(len(absPath))
	for _, r := range absPath {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
