package parser

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"
)

// Message parsing constants
const (
	MaxMessageLength = 512
	MaxTagLength     = 8192
)

const (
	LogTrace = -8
)

type Tags map[string]string

type Message struct {
	Tags    Tags
	Source  string
	Command string
	Params  []string
	Raw     string
}

func ParseMessage(raw string) (*Message, error) {
	slog.Debug("Parsing IRC message", "raw_length", len(raw))

	if !utf8.ValidString(raw) {
		slog.Debug("Message parsing failed: invalid UTF-8", "raw_length", len(raw))
		return nil, NewMessageError("ParseMessage", "message contains invalid UTF-8 encoding", nil)
	}

	// This should really only check MaxMessageLength, and count tags separately, this avoids any memory issues
	// allows some bad implementations to get away with a few mistakes and doesn't post any real issues.
	if len(raw) > MaxMessageLength+MaxTagLength {
		slog.Debug("Message parsing: too long", "length", len(raw), "max_length", MaxMessageLength, "max_tag_length", MaxTagLength)
		return nil, NewMessageError("ParseMessage", fmt.Sprintf("message exceeds maximum length: %d bytes", len(raw)), nil)
	}

	raw = strings.TrimSuffix(raw, "\r\n")
	if raw == "" {
		slog.Debug("Message parsing failed: empty after trimming")
		return nil, NewMessageError("ParseMessage", "message is empty after processing", nil)
	}

	msg := &Message{
		Tags: make(Tags),
		Raw:  raw,
	}

	pos := 0

	if raw[0] == '@' {
		pos = strings.Index(raw, " ")
		if pos == -1 {
			slog.Debug("Message parsing failed: tags without command")
			return nil, NewMessageError("ParseMessage", "IRC message contains tags but no command", nil)
		}

		tagsStr := raw[1:pos]
		slog.Debug("Parsing IRCv3 tags", "tags_string", tagsStr)
		if err := msg.parseTags(tagsStr); err != nil {
			slog.Debug("Tag parsing failed", "error", err, "tags_string", tagsStr)
			return nil, NewMessageError("ParseMessage", "failed to parse IRC message tags", err)
		}
		slog.Debug("Parsed IRCv3 tags", "tag_count", len(msg.Tags))
		pos++
	}

	if pos < len(raw) && raw[pos] == ':' {
		spacePos := strings.Index(raw[pos:], " ")
		if spacePos == -1 {
			return nil, NewMessageError("ParseMessage", "IRC message contains source but no command", nil)
		}

		msg.Source = raw[pos+1 : pos+spacePos]
		pos += spacePos + 1
	}

	if pos >= len(raw) {
		return nil, NewMessageError("ParseMessage", "IRC message missing required command", nil)
	}

	remaining := raw[pos:]
	parts := strings.Split(remaining, " ")
	if len(parts) == 0 {
		return nil, NewMessageError("ParseMessage", "IRC message missing required command", nil)
	}

	msg.Command = strings.ToUpper(parts[0])

	if len(parts) > 1 {
		msg.Params = make([]string, 0, len(parts)-1)
		for i := 1; i < len(parts); i++ {
			if parts[i] == "" {
				continue
			}
			if parts[i][0] == ':' {
				trailing := strings.Join(parts[i:], " ")
				msg.Params = append(msg.Params, trailing[1:])
				break
			}
			msg.Params = append(msg.Params, parts[i])
		}
	}

	slog.Log(context.Background(), LogTrace, "<-- "+raw)
	slog.Debug("Message parsed successfully",
		"command", msg.Command,
		"source", msg.Source,
		"param_count", len(msg.Params),
		"tag_count", len(msg.Tags),
		"is_numeric", msg.IsNumeric(),
	)

	return msg, nil
}

func (m *Message) parseTags(tagsStr string) error {
	if tagsStr == "" {
		return nil
	}

	pairs := strings.Split(tagsStr, ";")
	for _, pair := range pairs {
		if pair == "" {
			continue
		}

		if strings.Contains(pair, "=") {
			parts := strings.SplitN(pair, "=", 2)
			key := parts[0]
			value := parts[1]

			if key == "" {
				return NewMessageError("parseTags", "IRC message tag has empty key", nil)
			}

			unescapedValue := unescapeTagValue(value)
			m.Tags[key] = unescapedValue
		} else {
			m.Tags[pair] = ""
		}
	}

	return nil
}

func unescapeTagValue(value string) string {
	value = strings.ReplaceAll(value, "\\:", ";")
	value = strings.ReplaceAll(value, "\\s", " ")
	value = strings.ReplaceAll(value, "\\\\", "\\")
	value = strings.ReplaceAll(value, "\\r", "\r")
	value = strings.ReplaceAll(value, "\\n", "\n")
	value = strings.ReplaceAll(value, "\\0", "\x00")
	return value
}

func (m *Message) String() string {
	var parts []string

	if len(m.Tags) > 0 {
		var tagParts []string
		for key, value := range m.Tags {
			if value == "" {
				tagParts = append(tagParts, key)
			} else {
				escapedValue := escapeTagValue(value)
				tagParts = append(tagParts, fmt.Sprintf("%s=%s", key, escapedValue))
			}
		}
		parts = append(parts, fmt.Sprintf("@%s", strings.Join(tagParts, ";")))
	}

	if m.Source != "" {
		parts = append(parts, fmt.Sprintf(":%s", m.Source))
	}

	parts = append(parts, m.Command)

	if len(m.Params) > 0 {
		for i, param := range m.Params {
			if i == len(m.Params)-1 && (strings.Contains(param, " ") || param == "" || param[0] == ':') {
				parts = append(parts, fmt.Sprintf(":%s", param))
			} else {
				parts = append(parts, param)
			}
		}
	}

	return strings.Join(parts, " ")
}

func escapeTagValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, ";", "\\:")
	value = strings.ReplaceAll(value, " ", "\\s")
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\x00", "\\0")
	return value
}

func (m *Message) GetTag(key string) (string, bool) {
	value, exists := m.Tags[key]
	return value, exists
}

func (m *Message) HasTag(key string) bool {
	_, exists := m.Tags[key]
	return exists
}

func (m *Message) IsNumeric() bool {
	if len(m.Command) != 3 {
		return false
	}
	for _, char := range m.Command {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (m *Message) ParamCount() int {
	return len(m.Params)
}

func (m *Message) GetParam(index int) string {
	if index < 0 || index >= len(m.Params) {
		return ""
	}
	return m.Params[index]
}

func (m *Message) GetTrailing() string {
	if len(m.Params) == 0 {
		return ""
	}
	return m.Params[len(m.Params)-1]
}
