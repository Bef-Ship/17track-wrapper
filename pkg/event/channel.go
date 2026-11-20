package event

import (
	"fmt"
	"strings"
	"unicode"
)

// Channel format: {sourceType}.{source}  e.g. external.wechat, internal.user
func Channel(sourceType SourceType, source string) (string, error) {
	src, err := NormalizeSource(source)
	if err != nil {
		return "", err
	}
	switch sourceType {
	case SourceExternal, SourceInternal:
	default:
		return "", fmt.Errorf("event channel: unknown sourceType %q", sourceType)
	}
	return string(sourceType) + "." + src, nil
}

func NormalizeSource(source string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(source))
	if s == "" {
		return "", fmt.Errorf("event channel: empty source")
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		return "", fmt.Errorf("event channel: invalid source %q", source)
	}
	return s, nil
}

func ParseChannel(channel string) (SourceType, string, error) {
	channel = strings.TrimSpace(channel)
	typeName, source, ok := strings.Cut(channel, ".")
	if !ok || typeName == "" || source == "" || strings.Contains(source, ".") {
		return "", "", fmt.Errorf("event channel: want {type}.{source}, got %q", channel)
	}
	st := SourceType(typeName)
	if st != SourceExternal && st != SourceInternal {
		return "", "", fmt.Errorf("event channel: unknown type %q", typeName)
	}
	src, err := NormalizeSource(source)
	if err != nil {
		return "", "", err
	}
	return st, src, nil
}

func ValidateChannel(channel string, sourceType SourceType) error {
	st, _, err := ParseChannel(channel)
	if err != nil {
		return err
	}
	if st != sourceType {
		return fmt.Errorf("event channel %q does not match sourceType %s", channel, sourceType)
	}
	return nil
}
