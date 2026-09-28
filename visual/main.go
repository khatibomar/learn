// Command learn-visual renders Mermaid and SVG diagrams to PNG with headless
// Firefox. It serves the six diagram tools and the quiz tool over MCP for any
// agent, and runs single diagram tool calls for the pi extension.
//
//	learn-visual mcp                            MCP server on stdio
//	learn-visual tools                          tool definitions as JSON
//	learn-visual call <tool> --state <dir>      one call, JSON args on stdin
//	learn-visual log link <file.md> | unlink | status
//	learn-visual hook <agent>                   md-log hook, hook JSON on stdin
//
// Published PNGs go to $LEARN_ROOT/viz, or ./viz when LEARN_ROOT is not set.
package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const usage = "usage: learn-visual mcp | tools | call <tool> --state <dir> | log link <file> | log unlink | log status | hook <agent>"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "learn-visual:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "mcp":
		return serveMCP(ctx)
	case "tools":
		return json.MarshalWrite(os.Stdout, Tools())
	case "call":
		return callOnce(ctx, args[1:])
	case "log":
		return logCommand(args[1:])
	case "hook":
		if len(args) != 2 {
			return errors.New(usage)
		}
		// A hook must not block the agent, so errors go to stderr only.
		if err := runHook(args[1], os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "learn-visual:", err)
		}
		return nil
	}
	return errors.New(usage)
}

func logCommand(args []string) error {
	root, err := learnRoot()
	if err != nil {
		return err
	}
	var msg string
	switch {
	case len(args) >= 1 && args[0] == "link":
		msg, err = logLink(root, strings.TrimSpace(strings.Join(args[1:], " ")))
	case len(args) == 1 && args[0] == "unlink":
		msg, err = logUnlink(root)
	case len(args) == 1 && args[0] == "status":
		msg, err = logStatus(root)
	default:
		return errors.New(usage)
	}
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

func vizDir() (string, error) {
	root, err := learnRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "viz"), nil
}

func findTool(name string) (*Tool, error) {
	for _, t := range Tools() {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

func callOnce(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	tool, err := findTool(args[0])
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	state := fs.String("state", "", "directory that holds the session source file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *state == "" {
		return errors.New("call: --state is required")
	}
	viz, err := vizDir()
	if err != nil {
		return err
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	params := map[string]string{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &params); err != nil {
			return fmt.Errorf("call: arguments: %w", err)
		}
	}
	s := &Session{stateDir: *state, vizDir: viz}
	return json.MarshalWrite(os.Stdout, s.Call(ctx, tool, params))
}

func serveMCP(ctx context.Context) error {
	viz, err := vizDir()
	if err != nil {
		return err
	}
	state, err := os.MkdirTemp("", "learn-visual-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(state)
	s := &Session{stateDir: state, vizDir: viz}

	server := mcp.NewServer(&mcp.Implementation{Name: "learn-visual", Version: "0.2.0"}, nil)
	for _, t := range Tools() {
		server.AddTool(&mcp.Tool{
			Name:        t.Name,
			Title:       t.Label,
			Description: t.Description,
			InputSchema: t.Schema(),
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			params := map[string]string{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
					return nil, fmt.Errorf("arguments: %w", err)
				}
			}
			return toMCP(s.Call(ctx, t, params)), nil
		})
	}
	addQuizTool(server)
	addLogTool(server)
	return server.Run(ctx, &mcp.StdioTransport{})
}

func toMCP(r Result) *mcp.CallToolResult {
	out := &mcp.CallToolResult{IsError: r.IsError}
	for _, c := range r.Content {
		switch c.Type {
		case "image":
			out.Content = append(out.Content, &mcp.ImageContent{Data: c.Data, MIMEType: c.MIMEType})
		default:
			out.Content = append(out.Content, &mcp.TextContent{Text: c.Text})
		}
	}
	return out
}
