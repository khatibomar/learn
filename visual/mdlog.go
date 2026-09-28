package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// logState is the md-log link of one project. It lives in <root>/.learn/md-log.json.
type logState struct {
	File string `json:"file"`
	// Offsets holds, for each transcript, the byte offset up to which it is logged.
	Offsets map[string]int64 `json:"offsets,omitzero"`
	// Asked holds the IDs of question tool calls whose question is logged and whose answer is not.
	Asked map[string]bool `json:"asked,omitzero"`
}

func learnRoot() (string, error) {
	if root := os.Getenv("LEARN_ROOT"); root != "" {
		return root, nil
	}
	return os.Getwd()
}

func statePath(root string) string { return filepath.Join(root, ".learn", "md-log.json") }

// withLog runs fn with the project log state under a file lock, and saves the
// state after fn. It does nothing when no file is linked.
func withLog(root string, fn func(st *logState, w *logWriter) error) error {
	unlock, err := lockState(root)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := loadState(root)
	if err != nil || st == nil {
		return err
	}
	w := &logWriter{file: st.File}
	if err := fn(st, w); err != nil {
		return err
	}
	if err := w.flush(); err != nil {
		return err
	}
	return saveState(root, st)
}

func loadState(root string) (*logState, error) {
	data, err := os.ReadFile(statePath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := &logState{}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("md-log state: %w", err)
	}
	if st.File == "" {
		return nil, nil
	}
	if st.Offsets == nil {
		st.Offsets = map[string]int64{}
	}
	if st.Asked == nil {
		st.Asked = map[string]bool{}
	}
	return st, nil
}

func saveState(root string, st *logState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := statePath(root) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(root))
}

// lockState takes an exclusive lock with a lock directory, because hooks and
// the MCP server run as separate processes.
func lockState(root string) (func(), error) {
	dir := filepath.Join(root, ".learn")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lock := filepath.Join(dir, "md-log.lock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := os.Mkdir(lock, 0o755)
		if err == nil {
			return func() { os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > 30*time.Second {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("md-log: lock busy: " + lock)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// logLink links the markdown file. The file must exist, so that a wrong path
// does not make new files in the vault. Each transcript is logged from its
// start, so the current session is backfilled.
func logLink(root, file string) (string, error) {
	if file == "" {
		return "", errors.New("usage: learn-visual log link <file.md>")
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	if fi, err := os.Stat(file); err != nil || fi.IsDir() {
		return "", fmt.Errorf("md-log: file does not exist: %s", file)
	}
	unlock, err := lockState(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := saveState(root, &logState{File: file}); err != nil {
		return "", err
	}
	return "md-log: linked " + file + ". The session is backfilled at the end of this turn.", nil
}

func logUnlink(root string) (string, error) {
	unlock, err := lockState(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := os.Remove(statePath(root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return "md-log: stopped logging.", nil
}

func logStatus(root string) (string, error) {
	st, err := loadState(root)
	if err != nil {
		return "", err
	}
	if st == nil {
		return "md-log: no file linked.", nil
	}
	return "md-log: linked " + st.File, nil
}

// LogInput is the input of the md_log tool.
type LogInput struct {
	Action string `json:"action" jsonschema:"link, unlink, or status."`
	File   string `json:"file,omitempty" jsonschema:"For link: the markdown file, absolute or relative to the project root. The file must exist."`
}

func addLogTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:  "md_log",
		Title: "md-log",
		Description: "Link a markdown file (for example an Obsidian note) to the session. Hooks then append " +
			"each user prompt, each reply, and each quiz to the file, and the current session is backfilled. " +
			"Call it when the user says /md-log <file>, \"log to <file>\", /md-unlog, or \"stop logging\". " +
			"Do not write to the file yourself. Reply to the user with the result in one line.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in LogInput) (*mcp.CallToolResult, any, error) {
		root, err := learnRoot()
		if err != nil {
			return nil, nil, err
		}
		var msg string
		switch in.Action {
		case "link":
			msg, err = logLink(root, strings.TrimSpace(in.File))
		case "unlink":
			msg, err = logUnlink(root)
		case "status":
			msg, err = logStatus(root)
		default:
			err = fmt.Errorf("md_log: unknown action %q", in.Action)
		}
		if err != nil {
			return toolText(true, err.Error()), nil, nil
		}
		return toolText(false, msg), nil, nil
	})
}

// logWriter collects blocks and appends them to the log file in one write.
type logWriter struct {
	file   string
	blocks []string
}

func (w *logWriter) add(block string) {
	if block = strings.TrimSpace(block); block != "" {
		w.blocks = append(w.blocks, block)
	}
}

func (w *logWriter) flush() error {
	if len(w.blocks) == 0 {
		return nil
	}
	current, err := os.ReadFile(w.file)
	if err != nil {
		return fmt.Errorf("md-log: %w", err)
	}
	f, err := os.OpenFile(w.file, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("md-log: %w", err)
	}
	defer f.Close()
	text := strings.Join(w.blocks, "\n\n") + "\n"
	if strings.TrimSpace(string(current)) != "" {
		text = "\n\n" + text
		if strings.HasSuffix(string(current), "\n") {
			text = text[1:]
		}
	}
	w.blocks = nil
	_, err = f.WriteString(text)
	return err
}

func callout(kind, title string, body []string) string {
	lines := []string{fmt.Sprintf("> [!%s] %s", kind, title)}
	for _, l := range body {
		if l == "" {
			lines = append(lines, ">")
		} else {
			lines = append(lines, "> "+l)
		}
	}
	return strings.Join(lines, "\n")
}

var (
	skillBlock  = regexp.MustCompile(`(?s)<skill\b([^>]*)>.*?</skill>`)
	skillName   = regexp.MustCompile(`name="([^"]+)"`)
	pastedBlock = regexp.MustCompile(`(?s)<pasted_content\b[^>]*>\n?(.*?)\n?</pasted_content[^>]*>`)
)

func userBlock(text string) string {
	text = strings.TrimSpace(text)
	text = skillBlock.ReplaceAllStringFunc(text, func(m string) string {
		name := "(unknown)"
		if s := skillName.FindStringSubmatch(skillBlock.FindStringSubmatch(m)[1]); s != nil {
			name = s[1]
		}
		return "> [!note] SKILL loaded: " + name
	})
	text = pastedBlock.ReplaceAllStringFunc(text, func(m string) string {
		body := strings.Split(dedent(pastedBlock.FindStringSubmatch(m)[1]), "\n")
		return callout("info", "Pasted", body)
	})
	return "> [!quote] YOU\n\n" + text
}

func assistantBlock(agent, text string) string {
	return fmt.Sprintf("> [!abstract] %s\n\n%s", strings.ToUpper(agent), strings.TrimSpace(text))
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	prefix := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if prefix < 0 || n < prefix {
			prefix = n
		}
	}
	for i, l := range lines {
		if len(l) >= prefix && prefix > 0 {
			lines[i] = l[prefix:]
		}
	}
	return strings.Join(lines, "\n")
}

func questionCallout(title, question, details string, options []string) string {
	body := strings.Split(strings.TrimSpace(question), "\n")
	if d := strings.TrimSpace(details); d != "" {
		body = append(append(body, ""), strings.Split(d, "\n")...)
	}
	if len(options) > 0 {
		body = append(append(body, ""), options...)
	}
	return callout("question", title, body)
}
