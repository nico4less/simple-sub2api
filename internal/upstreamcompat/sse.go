package upstreamcompat

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

type SSEDataAccumulator struct {
	lines []string
}

func (a *SSEDataAccumulator) AddLine(line string, fn func([]byte)) {
	if fn == nil {
		return
	}
	trimmedLine := strings.TrimRight(line, "\r\n")
	if data, ok := ExtractOpenAISSEDataLine(trimmedLine); ok {
		a.lines = append(a.lines, data)
		return
	}
	if strings.TrimSpace(trimmedLine) == "" {
		a.Flush(fn)
	}
}

func (a *SSEDataAccumulator) Flush(fn func([]byte)) {
	if fn == nil || len(a.lines) == 0 {
		return
	}
	EmitOpenAISSEDataPayloads(a.lines, fn)
	a.lines = a.lines[:0]
}

func ForEachOpenAISSEDataPayload(body string, fn func([]byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var acc SSEDataAccumulator
	for _, line := range strings.Split(body, "\n") {
		acc.AddLine(line, fn)
	}
	acc.Flush(fn)
}

func ForEachOpenAISSEDataPayloadFromReader(r io.Reader, fn func([]byte)) error {
	if fn == nil {
		return nil
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var acc SSEDataAccumulator
	for scanner.Scan() {
		acc.AddLine(scanner.Text(), fn)
	}
	acc.Flush(fn)
	return scanner.Err()
}

func ExtractOpenAISSEDataLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimLeft(line[len("data:"):], " \t"), true
}

func EmitOpenAISSEDataPayloads(lines []string, fn func([]byte)) {
	if fn == nil || len(lines) == 0 {
		return
	}
	if len(lines) == 1 {
		EmitOpenAISSEDataPayload(lines[0], fn)
		return
	}
	joined := strings.Join(lines, "\n")
	if jsonLike(joined) {
		EmitOpenAISSEDataPayload(joined, fn)
		return
	}
	for _, line := range lines {
		EmitOpenAISSEDataPayload(line, fn)
	}
}

func EmitOpenAISSEDataPayload(data string, fn func([]byte)) {
	if fn == nil {
		return
	}
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	fn([]byte(data))
}

func WriteSSEData(w io.Writer, payload []byte) error {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	_, err := w.Write(append(append([]byte("data: "), payload...), '\n', '\n'))
	return err
}

func WriteSSEDone(w io.Writer) error {
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	return err
}

func jsonLike(data string) bool {
	trimmed := strings.TrimSpace(data)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}
