package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/term"
)

func prompt(label, def string) string {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	value, err := readTextPrompt(ctx, os.Stdin, os.Stdout, label, def)
	if err != nil {
		panic(err)
	}
	return value
}

// Relay keystrokes without reading past the end of this prompt. The caller can
// stop the relay on timeout or interruption without leaving a stdin reader behind.
func relayPromptInput(input *os.File, out io.Writer) {
	terminal := term.IsTerminal(int(input.Fd()))
	var key [1]byte
	for {
		n, err := input.Read(key[:])
		if n != 1 || err != nil {
			return
		}
		switch key[0] {
		case 3, 4, 27: // Ctrl+C, Ctrl+D, Escape
			return
		case '\r':
			if !terminal { // Consume the LF in redirected CRLF input.
				continue
			}
			key[0] = '\n'
		}
		if _, err := out.Write(key[:]); err != nil || key[0] == '\n' {
			return
		}
	}
}

func readTextPrompt(ctx context.Context, input, output *os.File, label, def string) (string, error) {
	fd := int(input.Fd())
	terminalInput := term.IsTerminal(fd)
	terminal := terminalInput && term.IsTerminal(int(output.Fd()))
	if terminalInput {
		state, err := term.MakeRaw(fd)
		if err != nil {
			return "", fmt.Errorf("prepare terminal input: %w", err)
		}
		defer term.Restore(fd, state)
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	child := exec.CommandContext(ctx, exe, "--prompt-child")
	child.Stdin = input
	stream, err := child.StdoutPipe()
	if err != nil {
		return "", err
	}
	defer stream.Close()
	if err = child.Start(); err != nil {
		return "", err
	}
	defer func() {
		stop()
		_ = child.Wait()
	}()

	lineEnd := "\n"
	if terminal {
		lineEnd = "\r\n"
	}
	if def == "" {
		fmt.Fprintf(output, "  %s%s", label, lineEnd)
	} else {
		fmt.Fprintf(output, "  %s [%s]%s", label, def, lineEnd)
	}
	reader := bufio.NewReader(stream)
	if def != "" {
		timedOut, err := waitForTextInput(ctx, reader, output, terminal)
		if err != nil {
			return "", err
		}
		if timedOut {
			return def, nil
		}
	}
	echo := io.Writer(io.Discard)
	if terminal {
		echo = output
	}
	editor := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{reader, echo}, "  > ")
	if terminal {
		if width, height, err := term.GetSize(int(output.Fd())); err == nil && width > 0 && height > 0 {
			_ = editor.SetSize(width, height)
		}
	}
	value, err := editor.ReadLine()
	if err != nil || ctx.Err() != nil {
		if terminal {
			fmt.Fprint(output, lineEnd)
		}
		return "", fmt.Errorf("input closed or cancelled")
	}
	if value = strings.TrimSpace(value); value == "" {
		return def, nil
	}
	return value, nil
}

func waitForTextInput(ctx context.Context, reader *bufio.Reader, w io.Writer, terminal bool) (bool, error) {
	// Peek preserves the first character for the line editor.
	ready := make(chan error, 1)
	go func() {
		_, err := reader.Peek(1)
		ready <- err
	}()

	if terminal {
		defer finishCountdown(w, true)
	} else {
		fmt.Fprintln(w, "  Enter accepts the default. Typing stops the five-second countdown.")
	}
	tick := time.NewTicker(countdownStep)
	defer tick.Stop()

	for seconds := 5; seconds > 0; seconds-- {
		if terminal {
			fmt.Fprintf(w, "\r  Default in %ds. Type to override or press Enter.", seconds)
		}
		select {
		case err := <-ready:
			if err != nil || ctx.Err() != nil {
				return false, fmt.Errorf("input closed or cancelled")
			}
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		case <-tick.C:
		}
	}
	// Give input already observed on the final tick precedence over the default.
	select {
	case err := <-ready:
		if err != nil || ctx.Err() != nil {
			return false, fmt.Errorf("input closed or cancelled")
		}
		return false, nil
	default:
		return ctx.Err() == nil, ctx.Err()
	}
}
