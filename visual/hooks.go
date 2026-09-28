package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// A chat event is one item of a session that the md-log file shows.
type chatEvent struct {
	kind string // "user", "assistant", "break", "ask", "answer"
	text string
	id   string        // tool call ID, for "ask" and "answer"
	ask  []askQuestion // for "ask"
}

type askQuestion struct {
	Question    string `json:"question"`
	Header      string `json:"header"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// writeEvents appends the events to the log. Consecutive assistant texts
// become one block. A "break" ends the current assistant block.
func writeEvents(agent string, st *logState, w *logWriter, events []chatEvent) {
	var pending []string
	flushText := func() {
		if len(pending) > 0 {
			w.add(assistantBlock(agent, strings.Join(pending, "\n\n")))
			pending = nil
		}
	}
	for _, e := range events {
		switch e.kind {
		case "assistant":
			if t := strings.TrimSpace(e.text); t != "" {
				pending = append(pending, t)
			}
			continue
		case "user":
			flushText()
			w.add(userBlock(e.text))
		case "break":
			flushText()
		case "ask":
			flushText()
			if st.Asked[e.id] {
				continue
			}
			st.Asked[e.id] = true
			for _, q := range e.ask {
				w.add(askCallout(q))
			}
		case "answer":
			if !st.Asked[e.id] {
				continue
			}
			delete(st.Asked, e.id)
			flushText()
			body := strings.Split(strings.TrimSpace(e.text), "\n")
			if len(body) == 0 || body[0] == "" {
				body = []string{"(no answer)"}
			}
			w.add(callout("example", "Answer", body))
		}
	}
	flushText()
}

func hasText(events []chatEvent, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].kind == "assistant" {
			return strings.HasSuffix(text, strings.TrimSpace(events[i].text))
		}
	}
	return false
}

func askCallout(q askQuestion) string {
	title := "Question"
	if q.Header != "" {
		title += " — " + q.Header
	}
	opts := make([]string, len(q.Options))
	for i, o := range q.Options {
		opts[i] = fmt.Sprintf("%d. %s", i+1, o.Label)
		if o.Description != "" {
			opts[i] += " — " + o.Description
		}
	}
	details := ""
	if q.MultiSelect {
		details = "Select all that apply."
	}
	return questionCallout(title, q.Question, details, opts)
}

// readNewLines returns the complete lines of path after offset, and the new offset.
func readNewLines(path string, offset int64) ([][]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset, err
	}
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return nil, offset, nil
	}
	var lines [][]byte
	sc := bufio.NewScanner(bytes.NewReader(data[:end+1]))
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			lines = append(lines, bytes.Clone(line))
		}
	}
	return lines, offset + int64(end) + 1, sc.Err()
}

// hookPayload holds the hook input fields that the agents use.
type hookPayload struct {
	Event          string         `json:"hook_event_name"`
	Cwd            string         `json:"cwd"`
	TranscriptPath string         `json:"transcript_path"`
	ToolName       string         `json:"tool_name"`
	ToolInput      jsontext.Value `json:"tool_input"`
	ToolUseID      string         `json:"tool_use_id"`
	// LastAssistantMessage is the final text of a Claude Code turn.
	LastAssistantMessage string `json:"last_assistant_message"`
	// Prompt and PromptResponse are the prompt and the final text of a Gemini CLI turn.
	Prompt         string `json:"prompt"`
	PromptResponse string `json:"prompt_response"`
	// OpenCode: the session messages that the plugin sends.
	SessionID string          `json:"session_id"`
	Messages  []opencodeEntry `json:"messages"`
}

// runHook handles one hook event of an agent.
func runHook(agent string, stdin io.Reader) error {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	var p hookPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("hook %s: payload: %w", agent, err)
	}
	root := cmp.Or(os.Getenv("LEARN_ROOT"), p.Cwd)
	if root == "" {
		if root, err = os.Getwd(); err != nil {
			return err
		}
	}
	var handle func(st *logState, w *logWriter) error
	switch agent {
	case "claude":
		handle = func(st *logState, w *logWriter) error { return claudeHook(st, w, &p) }
	case "gemini":
		handle = func(st *logState, w *logWriter) error { geminiHook(w, &p); return nil }
	case "opencode":
		handle = func(st *logState, w *logWriter) error { opencodeHook(st, w, &p); return nil }
	default:
		return errors.New("hook: unknown agent " + agent)
	}
	return withLog(root, handle)
}

// geminiHook logs the prompt before a Gemini CLI turn and the final text after it.
func geminiHook(w *logWriter, p *hookPayload) {
	switch prompt, reply := strings.TrimSpace(p.Prompt), strings.TrimSpace(p.PromptResponse); {
	case p.Event == "BeforeAgent" && prompt != "" && !strings.HasPrefix(prompt, "/md-"):
		w.add(userBlock(prompt))
	case p.Event == "AfterAgent" && reply != "":
		w.add(assistantBlock("gemini", reply))
	}
}

// opencodeEntry is one message of an OpenCode session, as the SDK returns it.
type opencodeEntry struct {
	Info struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	} `json:"info"`
	Parts []struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Synthetic bool   `json:"synthetic"`
		Tool      string `json:"tool"`
	} `json:"parts"`
}

// opencodeHook logs the session messages after the last logged message.
func opencodeHook(st *logState, w *logWriter, p *hookPayload) {
	key := "opencode:" + p.SessionID
	var events []chatEvent
	for i, m := range p.Messages {
		if int64(i) < st.Offsets[key] {
			continue
		}
		for _, part := range m.Parts {
			switch {
			case part.Type == "text" && !part.Synthetic && m.Info.Role == "user":
				if t := strings.TrimSpace(part.Text); t != "" && !strings.HasPrefix(t, "/md-") {
					events = append(events, chatEvent{kind: "user", text: t})
				}
			case part.Type == "text" && !part.Synthetic && m.Info.Role == "assistant":
				events = append(events, chatEvent{kind: "assistant", text: part.Text})
			case part.Type == "tool" && strings.HasSuffix(part.Tool, "_quiz"):
				events = append(events, chatEvent{kind: "break"})
			}
		}
	}
	st.Offsets[key] = int64(len(p.Messages))
	writeEvents("opencode", st, w, events)
}

// claudeHook logs the new part of the Claude Code transcript. Before an
// AskUserQuestion call it also logs the question, so that the question is in
// the file while the user answers.
func claudeHook(st *logState, w *logWriter, p *hookPayload) error {
	if p.TranscriptPath == "" {
		return nil
	}
	var events []chatEvent
	// The transcript write can lag behind the Stop hook, so wait a short time
	// for the final text of the turn.
	for try := 0; ; try++ {
		lines, next, err := readNewLines(p.TranscriptPath, st.Offsets[p.TranscriptPath])
		if err != nil {
			return err
		}
		st.Offsets[p.TranscriptPath] = next
		for _, line := range lines {
			events = append(events, claudeEvents(line)...)
		}
		if p.Event != "Stop" || try == 20 || hasText(events, p.LastAssistantMessage) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if p.Event == "PreToolUse" && p.ToolName == "AskUserQuestion" && p.ToolUseID != "" {
		var in struct {
			Questions []askQuestion `json:"questions"`
		}
		if json.Unmarshal(p.ToolInput, &in) == nil {
			events = append(events, chatEvent{kind: "ask", id: p.ToolUseID, ask: in.Questions})
		}
	}
	writeEvents("claude", st, w, events)
	return nil
}

type claudeEntry struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content jsontext.Value `json:"content"`
	} `json:"message"`
}

type claudeBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Input     jsontext.Value `json:"input"`
	ToolUseID string         `json:"tool_use_id"`
	Content   jsontext.Value `json:"content"`
}

var (
	commandName = regexp.MustCompile(`<command-name>\s*(.*?)\s*</command-name>`)
	commandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// claudeEvents turns one transcript line into chat events.
func claudeEvents(line []byte) []chatEvent {
	var e claudeEntry
	if json.Unmarshal(line, &e) != nil || e.IsSidechain || e.IsMeta || len(e.Message.Content) == 0 {
		return nil
	}
	var text string
	var blocks []claudeBlock
	if json.Unmarshal(e.Message.Content, &text) != nil {
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			return nil
		}
	}
	switch e.Type {
	case "user":
		if text == "" {
			var parts []string
			for _, b := range blocks {
				switch b.Type {
				case "text":
					parts = append(parts, b.Text)
				case "tool_result":
					return []chatEvent{{kind: "answer", id: b.ToolUseID, text: blockText(b.Content)}}
				}
			}
			text = strings.Join(parts, "\n\n")
		}
		if text = claudePrompt(text); text == "" {
			return nil
		}
		return []chatEvent{{kind: "user", text: text}}
	case "assistant":
		var out []chatEvent
		for _, b := range blocks {
			switch {
			case b.Type == "text":
				out = append(out, chatEvent{kind: "assistant", text: b.Text})
			case b.Type == "tool_use" && b.Name == "AskUserQuestion":
				var in struct {
					Questions []askQuestion `json:"questions"`
				}
				json.Unmarshal(b.Input, &in)
				out = append(out, chatEvent{kind: "ask", id: b.ID, ask: in.Questions})
			case b.Type == "tool_use" && strings.HasSuffix(b.Name, "__quiz"):
				out = append(out, chatEvent{kind: "break"})
			}
		}
		return out
	}
	return nil
}

// claudePrompt returns the text of a user prompt as the user typed it, or ""
// for text that the harness adds.
func claudePrompt(text string) string {
	t := strings.TrimSpace(text)
	if m := commandName.FindStringSubmatch(t); m != nil {
		name := m[1]
		if strings.HasPrefix(name, "/md-") {
			return ""
		}
		if a := commandArgs.FindStringSubmatch(t); a != nil && strings.TrimSpace(a[1]) != "" {
			return name + " " + strings.TrimSpace(a[1])
		}
		return name
	}
	for _, p := range []string{"<local-command", "<bash-", "<task-notification", "<system-reminder", "[Request interrupted"} {
		if strings.HasPrefix(t, p) {
			return ""
		}
	}
	return t
}

// blockText returns the text of a tool result content, a string or a list of text blocks.
func blockText(v jsontext.Value) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var blocks []claudeBlock
	json.Unmarshal(v, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
