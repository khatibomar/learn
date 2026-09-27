package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const renderTimeout = 120 * time.Second

// pageScale is the device pixel ratio of the screenshot, for a crisp PNG.
const pageScale = 2

const pageHTML = `<!doctype html>
<html><head><meta charset="utf-8">
<style>html,body{margin:0;background:#fff}#out{display:inline-block;padding:16px}</style>
%s</head><body><div id="out"></div></body></html>`

// Both scripts put the rendered SVG into #out and return a JSON string:
// {"x","y","w","h"} of #out on success, or {"error"} on failure.
const mermaidScript = `(async () => {
  try {
    mermaid.initialize({ startOnLoad: false });
    const { svg } = await mermaid.render("diagram", %s);
    const out = document.getElementById("out");
    out.innerHTML = svg;
    const el = out.querySelector("svg");
    const vb = el.viewBox.baseVal;
    if (vb && vb.width) {
      el.setAttribute("width", vb.width);
      el.setAttribute("height", vb.height);
    }
    el.style.maxWidth = "none";
    await document.fonts.ready;
    const r = out.getBoundingClientRect();
    return JSON.stringify({ x: r.x, y: r.y, w: r.width, h: r.height });
  } catch (e) {
    return JSON.stringify({ error: String((e && e.message) || e) });
  }
})()`

const svgScript = `(async () => {
  const doc = new DOMParser().parseFromString(%s, "image/svg+xml");
  const bad = doc.querySelector("parsererror");
  if (bad) return JSON.stringify({ error: bad.textContent.split("\n").filter((l) => !l.startsWith("Location:")).join("\n") });
  const el = document.importNode(doc.documentElement, true);
  if (el.localName !== "svg") return JSON.stringify({ error: "root element is <" + el.localName + ">, not <svg>" });
  const vb = el.viewBox.baseVal;
  if (!el.hasAttribute("width") && vb && vb.width) {
    el.setAttribute("width", vb.width);
    el.setAttribute("height", vb.height);
  }
  const out = document.getElementById("out");
  out.appendChild(el);
  await document.fonts.ready;
  const r = out.getBoundingClientRect();
  return JSON.stringify({ x: r.x, y: r.y, w: r.width, h: r.height });
})()`

func renderMermaid(ctx context.Context, source, outPath string) error {
	lib, err := mermaidJS(ctx)
	if err != nil {
		return err
	}
	head := fmt.Sprintf(`<script src="%s"></script>`, fileURL(lib))
	return renderPage(ctx, head, fmt.Sprintf(mermaidScript, jsString(source)), outPath)
}

func renderSVG(ctx context.Context, source, outPath string) error {
	return renderPage(ctx, "", fmt.Sprintf(svgScript, jsString(source)), outPath)
}

// renderPage loads a blank page in headless Firefox, runs script to draw into
// it, and saves a PNG of the drawn area to outPath.
func renderPage(ctx context.Context, head, script, outPath string) error {
	ctx, cancel := context.WithTimeoutCause(ctx, renderTimeout, errors.New("firefox render timed out"))
	defer cancel()

	page := filepath.Join(filepath.Dir(outPath), "page.html")
	if err := os.WriteFile(page, fmt.Appendf(nil, pageHTML, head), 0o644); err != nil {
		return err
	}

	ff, err := startFirefox(ctx)
	if err != nil {
		return err
	}
	defer ff.close()

	b := ff.bidi
	if _, err := b.call(ctx, "session.new", map[string]any{"capabilities": map[string]any{}}); err != nil {
		return err
	}
	var tree struct {
		Contexts []struct {
			Context string `json:"context"`
		} `json:"contexts"`
	}
	if err := b.callInto(ctx, "browsingContext.getTree", map[string]any{}, &tree); err != nil {
		return err
	}
	if len(tree.Contexts) == 0 {
		return errors.New("firefox has no browsing context")
	}
	bc := tree.Contexts[0].Context

	if _, err := b.call(ctx, "browsingContext.setViewport", map[string]any{
		"context":          bc,
		"viewport":         map[string]any{"width": 1600, "height": 1200},
		"devicePixelRatio": pageScale,
	}); err != nil {
		return err
	}
	if _, err := b.call(ctx, "browsingContext.navigate", map[string]any{
		"context": bc, "url": fileURL(page), "wait": "complete",
	}); err != nil {
		return err
	}

	var eval struct {
		Type   string `json:"type"`
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := b.callInto(ctx, "script.evaluate", map[string]any{
		"expression": script, "target": map[string]any{"context": bc}, "awaitPromise": true,
	}, &eval); err != nil {
		return err
	}
	if eval.Type != "success" {
		return fmt.Errorf("script failed: %s", eval.ExceptionDetails.Text)
	}
	var box struct {
		X, Y, W, H float64
		Error      string
	}
	if err := json.Unmarshal([]byte(eval.Result.Value), &box, json.MatchCaseInsensitiveNames(true)); err != nil {
		return fmt.Errorf("bad script result %q: %w", eval.Result.Value, err)
	}
	if box.Error != "" {
		return errors.New(box.Error)
	}

	var shot struct {
		Data string `json:"data"`
	}
	if err := b.callInto(ctx, "browsingContext.captureScreenshot", map[string]any{
		"context": bc,
		"origin":  "document",
		"clip":    map[string]any{"type": "box", "x": box.X, "y": box.Y, "width": box.W, "height": box.H},
	}, &shot); err != nil {
		return err
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, png, 0o644)
}

type firefox struct {
	cmd     *exec.Cmd
	profile string
	bidi    *bidiConn
	stderr  *tailBuffer
}

var bidiListening = regexp.MustCompile(`WebDriver BiDi listening on (ws://\S+)`)

func startFirefox(ctx context.Context) (*firefox, error) {
	bin, err := firefoxBinary()
	if err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "learn-visual-firefox-")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "--headless", "--no-remote", "--profile", profile,
		"--remote-debugging-port=0", "about:blank")
	cmd.WaitDelay = 5 * time.Second
	pipe, err := cmd.StderrPipe()
	if err != nil {
		os.RemoveAll(profile)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(profile)
		return nil, fmt.Errorf("start firefox: %w", err)
	}
	ff := &firefox{cmd: cmd, profile: profile, stderr: &tailBuffer{}}

	wsURL := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pipe)
		for sc.Scan() {
			line := sc.Text()
			ff.stderr.add(line)
			if m := bidiListening.FindStringSubmatch(line); m != nil {
				select {
				case wsURL <- m[1]:
				default:
				}
			}
		}
	}()

	select {
	case u := <-wsURL:
		conn, _, err := websocket.Dial(ctx, u+"/session", nil)
		if err != nil {
			ff.close()
			return nil, fmt.Errorf("connect to firefox: %w", err)
		}
		conn.SetReadLimit(256 << 20)
		ff.bidi = &bidiConn{conn: conn}
		return ff, nil
	case <-ctx.Done():
		ff.close()
		return nil, fmt.Errorf("firefox did not start: %w\n%s", context.Cause(ctx), ff.stderr)
	}
}

func (f *firefox) close() {
	if f.bidi != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		f.bidi.call(closeCtx, "browser.close", map[string]any{})
		cancel()
		f.bidi.conn.CloseNow()
	}
	f.cmd.Process.Kill()
	f.cmd.Wait()
	os.RemoveAll(f.profile)
}

func firefoxBinary() (string, error) {
	if bin := os.Getenv("LEARN_FIREFOX"); bin != "" {
		return bin, nil
	}
	if bin, err := exec.LookPath("firefox"); err == nil {
		return bin, nil
	}
	for _, bin := range []string{
		"/Applications/Firefox.app/Contents/MacOS/firefox",
		`C:\Program Files\Mozilla Firefox\firefox.exe`,
	} {
		if _, err := os.Stat(bin); err == nil {
			return bin, nil
		}
	}
	return "", errors.New("firefox not found: install Firefox or set LEARN_FIREFOX to its binary")
}

// bidiConn sends WebDriver BiDi commands one at a time and skips events.
type bidiConn struct {
	conn *websocket.Conn
	next int
}

func (b *bidiConn) call(ctx context.Context, method string, params any) (jsontext.Value, error) {
	b.next++
	id := b.next
	msg, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err := b.conn.Write(ctx, websocket.MessageText, msg); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	for {
		_, data, err := b.conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		var reply struct {
			ID      int            `json:"id"`
			Type    string         `json:"type"`
			Result  jsontext.Value `json:"result"`
			Error   string         `json:"error"`
			Message string         `json:"message"`
		}
		if err := json.Unmarshal(data, &reply); err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		if reply.ID != id {
			continue
		}
		if reply.Type == "error" {
			return nil, fmt.Errorf("%s: %s: %s", method, reply.Error, reply.Message)
		}
		return reply.Result, nil
	}
}

func (b *bidiConn) callInto(ctx context.Context, method string, params, out any) error {
	raw, err := b.call(ctx, method, params)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// tailBuffer keeps the last lines of Firefox stderr for error messages.
type tailBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (t *tailBuffer) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > 30 {
		t.lines = t.lines[len(t.lines)-30:]
	}
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

func fileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
