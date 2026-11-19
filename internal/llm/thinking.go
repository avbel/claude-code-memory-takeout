package llm

import (
	"fmt"
	"strings"
)

// ThinkingStyle selects how a reasoning request is expressed in the request
// body: OpenAI's flat field, OpenRouter's nested object, or nothing.
type ThinkingStyle string

const (
	StyleAuto       ThinkingStyle = "auto"
	StyleOpenAI     ThinkingStyle = "openai"
	StyleOpenRouter ThinkingStyle = "openrouter"
	StyleNone       ThinkingStyle = "none"
)

// normalizeStyle maps an empty style to auto and rejects anything unknown.
func normalizeStyle(raw string) (ThinkingStyle, error) {
	switch s := ThinkingStyle(strings.ToLower(strings.TrimSpace(raw))); s {
	case "", StyleAuto:
		return StyleAuto, nil
	case StyleOpenAI:
		return StyleOpenAI, nil
	case StyleOpenRouter:
		return StyleOpenRouter, nil
	case StyleNone:
		return StyleNone, nil
	default:
		return "", fmt.Errorf("%w: unknown thinking style %q (want auto, openai, openrouter or none)", ErrUsage, raw)
	}
}

// normalizeLevel maps an empty level to "unset" (no thinking field sent) and
// rejects anything outside the known set.
func normalizeLevel(raw string) (string, error) {
	switch l := strings.ToLower(strings.TrimSpace(raw)); l {
	case "", "off", "minimal", "low", "medium", "high", "xhigh":
		return l, nil
	default:
		return "", fmt.Errorf("%w: unknown thinking level %q (want off, minimal, low, medium, high or xhigh)", ErrUsage, raw)
	}
}

// resolveStyle turns the auto style into a concrete one from the base URL
// host; an explicit style is kept as-is.
func resolveStyle(style ThinkingStyle, host string) ThinkingStyle {
	if style != StyleAuto {
		return style
	}
	if strings.HasSuffix(host, "openrouter.ai") {
		return StyleOpenRouter
	}
	return StyleOpenAI
}

// applyThinking adds the style-specific fields to the request body.
// An empty level means the user never asked for thinking: nothing is sent.
// For openai, off omits the field; for openrouter, off explicitly disables.
func applyThinking(body map[string]any, style ThinkingStyle, level string) {
	if style == StyleNone || level == "" {
		return
	}
	switch style {
	case StyleOpenAI:
		if level != "off" {
			body["reasoning_effort"] = level
		}
	case StyleOpenRouter:
		if level == "off" {
			body["reasoning"] = map[string]any{"enabled": false}
		} else {
			body["reasoning"] = map[string]any{"effort": level}
		}
	}
}
