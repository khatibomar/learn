package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Content is one item of a tool result, in MCP content shape.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitzero"`
	Data     []byte `json:"data,omitzero"`
	MIMEType string `json:"mimeType,omitzero"`
}

// Result is the outcome of one tool call.
type Result struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitzero"`
}

// Param is one string parameter of a tool.
type Param struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Optional    bool   `json:"optional,omitzero"`
}

// Tool describes one tool. The same definitions serve MCP and the pi extension.
type Tool struct {
	Name        string  `json:"name"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Params      []Param `json:"params"`
	kind        *kind
	op          string
}

// Schema returns the JSON Schema for the tool input.
func (t *Tool) Schema() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range t.Params {
		props[p.Name] = map[string]any{"type": "string", "description": p.Description}
		if !p.Optional {
			required = append(required, p.Name)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

type kind struct {
	name     string
	title    string
	bodyFile string
	render   func(ctx context.Context, source, outPath string) error
	validate func(source string) error
	// Hint texts that differ between the two kinds.
	writeDesc, renderLook, publishLook, slugExample, noun string
}

var mermaidKind = &kind{
	name:     "mermaid",
	title:    "Mermaid",
	bodyFile: "diagram.mmd",
	render:   renderMermaid,
	writeDesc: "`source` is a complete Mermaid diagram, e.g. a `graph TD` / `graph LR` " +
		"flow, `sequenceDiagram`, `stateDiagram-v2`, `erDiagram`, `classDiagram`, " +
		"`mindmap`, or `timeline`.",
	renderLook:  "LOOK: are arrows/relationships correct, labels right, nothing cramped?",
	publishLook: "LOOK at the diagram below to confirm it is correct before returning it.",
	slugExample: "internet-packets",
	noun:        "diagram",
}

var svgKind = &kind{
	name:     "svg",
	title:    "SVG",
	bodyFile: "diagram.svg",
	render:   renderSVG,
	validate: func(source string) error {
		if !strings.Contains(source, "<svg") {
			return errors.New("`write_svg`: source must be a complete <svg>…</svg> document")
		}
		return nil
	},
	writeDesc: "`source` is a complete `<svg ...>…</svg>` document with an explicit width/" +
		"height (or viewBox), readable font sizes, and a light or transparent background.",
	renderLook:  "LOOK: are coordinates, angles, directions, and proportions correct? Labels clear and unclipped?",
	publishLook: "LOOK at the picture below to confirm the geometry is correct before returning it.",
	slugExample: "number-line",
	noun:        "picture",
}

// Tools returns the six tool definitions.
func Tools() []*Tool {
	var out []*Tool
	for _, k := range []*kind{mermaidKind, svgKind} {
		n := k.name
		out = append(out,
			&Tool{
				Name:  "write_" + n,
				Label: "Write " + k.title,
				Description: fmt.Sprintf("Write the FULL %s source to this session's managed file (your first "+
					"draft or a complete rewrite). You do NOT name the file — edit_%s and render_%s act on "+
					"the same one.\n\n%s Writing does NOT render — call render_%s when ready. For a small "+
					"fix, prefer edit_%s over rewriting.", k.title, n, n, k.writeDesc, n, n),
				Params: []Param{{Name: "source", Description: "The complete " + k.title + " source."}},
				kind:   k, op: "write",
			},
			&Tool{
				Name:  "edit_" + n,
				Label: "Edit " + k.title,
				Description: fmt.Sprintf("Make a single exact-match replacement in this session's %s source. "+
					"`old_text` must appear EXACTLY ONCE (include surrounding context for uniqueness); on 0 "+
					"or >1 matches the call fails and nothing changes. Call write_%s first. Editing does "+
					"NOT render.", k.title, n),
				Params: []Param{
					{Name: "old_text", Description: "Exact substring of the current source to replace (must match once)."},
					{Name: "new_text", Description: "Replacement text for `old_text`."},
				},
				kind: k, op: "edit",
			},
			&Tool{
				Name:  "render_" + n,
				Label: "Render " + k.title,
				Description: fmt.Sprintf("Render the CURRENT session %s source to a PNG and return it inline so "+
					"you can SEE the %s and iterate. You do NOT pass the source here — it comes from the "+
					"managed file; call write_%s first.\n\nIterate freely with no `save_as` (preview only). "+
					"When the %s is correct and clean, call once more with `save_as` set to a short "+
					"kebab-case topic slug: that publishes the PNG into <cwd>/viz with a unique filename and "+
					"returns the filename to embed. On a render error this returns the error text instead "+
					"of an image — fix with edit_%s and re-render.", k.title, k.noun, n, k.noun, n),
				Params: []Param{{
					Name: "save_as",
					Description: fmt.Sprintf("Short kebab-case topic slug (e.g. '%s'). When set, the rendered "+
						"PNG is published to <cwd>/viz as viz-<slug>-<timestamp>.png and the filename is "+
						"returned. Omit for a preview-only render.", k.slugExample),
					Optional: true,
				}},
				kind: k, op: "render",
			},
		)
	}
	return out
}

// Session holds the managed source files in one state directory.
type Session struct {
	mu       sync.Mutex
	stateDir string
	vizDir   string
}

// Call runs one tool. Errors are returned as an error result, not as a Go error.
func (s *Session) Call(ctx context.Context, t *Tool, args map[string]string) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.call(ctx, t, args)
	if err != nil {
		return Result{Content: []Content{{Type: "text", Text: err.Error()}}, IsError: true}
	}
	return res
}

func (s *Session) call(ctx context.Context, t *Tool, args map[string]string) (Result, error) {
	k, n := t.kind, t.kind.name
	body := filepath.Join(s.stateDir, k.bodyFile)
	switch t.op {
	case "write":
		source := strings.TrimSpace(args["source"])
		if source == "" {
			return Result{}, fmt.Errorf("`write_%s` requires a non-empty `source`", n)
		}
		if k.validate != nil {
			if err := k.validate(source); err != nil {
				return Result{}, err
			}
		}
		if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(body, []byte(source), 0o644); err != nil {
			return Result{}, err
		}
		lines := strings.Count(source, "\n") + 1
		return textResult(fmt.Sprintf("Wrote %d-line %s source.\nCall render_%s to render it, or edit_%s to tweak it.",
			lines, k.title, n, n)), nil

	case "edit":
		current, err := os.ReadFile(body)
		if err != nil {
			return Result{}, fmt.Errorf("edit_%s: no source yet — call write_%s first", n, n)
		}
		updated, index, err := applyEdit(string(current), args["old_text"], args["new_text"])
		if err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(body, []byte(updated), 0o644); err != nil {
			return Result{}, err
		}
		return textResult("Applied edit. Updated region:\n```\n" + snippetAround(updated, index, 3) +
			"\n```\nCall render_" + n + " to see it."), nil

	case "render":
		source, err := os.ReadFile(body)
		if err != nil {
			return Result{}, fmt.Errorf("render_%s: no source yet — call write_%s first", n, n)
		}
		out := filepath.Join(s.stateDir, fmt.Sprintf("render-%d.png", time.Now().UnixMilli()))
		if err := k.render(ctx, string(source), out); err != nil {
			return Result{}, fmt.Errorf("%s render FAILED — no image produced. Fix the source with edit_%s "+
				"and call render_%s again.\n\nError:\n%s", k.title, n, n, lastLines(err.Error(), 30))
		}
		png, err := os.ReadFile(out)
		if err != nil {
			return Result{}, err
		}
		image := Content{Type: "image", Data: png, MIMEType: "image/png"}
		if slug := args["save_as"]; slug != "" {
			filename, path, err := s.publish(png, slug)
			if err != nil {
				return Result{}, err
			}
			return Result{Content: []Content{
				{Type: "text", Text: fmt.Sprintf("Published to viz/.\nfilename: %s\npath: %s\n\n%s", filename, path, k.publishLook)},
				image,
			}}, nil
		}
		return Result{Content: []Content{
			{Type: "text", Text: fmt.Sprintf("Preview render (not yet saved). %s Fix with edit_%s, or re-render "+
				"with `save_as` to publish.", k.renderLook, n)},
			image,
		}}, nil
	}
	return Result{}, fmt.Errorf("unknown operation %q", t.op)
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Session) publish(png []byte, slug string) (string, string, error) {
	clean := cmp.Or(strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(slug), "-"), "-"), "viz")
	if err := os.MkdirAll(s.vizDir, 0o755); err != nil {
		return "", "", err
	}
	filename := fmt.Sprintf("viz-%s-%d.png", clean, time.Now().UnixMilli())
	path := filepath.Join(s.vizDir, filename)
	return filename, path, os.WriteFile(path, png, 0o644)
}

func textResult(text string) Result {
	return Result{Content: []Content{{Type: "text", Text: text}}}
}

// applyEdit replaces oldText with newText. oldText must occur exactly once.
func applyEdit(current, oldText, newText string) (string, int, error) {
	if oldText == "" {
		return "", 0, errors.New("`old_text` must be non-empty")
	}
	if oldText == newText {
		return "", 0, errors.New("`old_text` and `new_text` are identical")
	}
	switch n := strings.Count(current, oldText); n {
	case 0:
		return "", 0, errors.New("`old_text` not found in the current source — match it exactly")
	case 1:
		i := strings.Index(current, oldText)
		return current[:i] + newText + current[i+len(oldText):], i, nil
	default:
		return "", 0, fmt.Errorf("`old_text` appears %d times — add surrounding context to make it unique", n)
	}
}

// snippetAround returns numbered lines around byte offset index.
func snippetAround(content string, index, context int) string {
	lines := strings.Split(content, "\n")
	hit := strings.Count(content[:index], "\n")
	start, end := max(0, hit-context), min(len(lines)-1, hit+context)
	width := len(fmt.Sprint(end + 1))
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%*d  %s", width, i+1, lines[i])
		if i < end {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
