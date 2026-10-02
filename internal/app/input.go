package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type requiredInput struct {
	name, supply string
	value        *string
}

const maxInteractiveInput = 64 << 10

func verbose(cmd *cobra.Command) bool {
	v, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	return v
}

func (a *App) interactive(cmd *cobra.Command) bool {
	disabled, _ := cmd.Root().PersistentFlags().GetBool("noninteractive")
	if disabled {
		return false
	}
	if a.IsTerminal != nil {
		return a.IsTerminal()
	}
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (a *App) requireInputs(cmd *cobra.Command, reader **bufio.Reader, inputs ...requiredInput) error {
	missing := make([]requiredInput, 0, len(inputs))
	for _, input := range inputs {
		if *input.value == "" {
			missing = append(missing, input)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !a.interactive(cmd) {
		parts := make([]string, len(missing))
		for i, input := range missing {
			parts[i] = input.name + " (supply " + input.supply + ")"
		}
		return errors.New("missing required input: " + strings.Join(parts, ", ") + "; use an interactive terminal without --noninteractive to be prompted")
	}
	if *reader == nil {
		*reader = bufio.NewReader(cmd.InOrStdin())
	}
	for _, input := range missing {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: ", input.name)
		value, err := readInteractiveLine(*reader)
		if err != nil {
			return fmt.Errorf("read %s: %w", input.name, err)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s is required; supply %s", input.name, input.supply)
		}
		*input.value = value
	}
	return nil
}

func readInteractiveLine(reader *bufio.Reader) (string, error) {
	line := make([]byte, 0, 256)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxInteractiveInput+2 {
			return "", fmt.Errorf("input exceeds maximum size of %d bytes", maxInteractiveInput)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			line = line[:len(line)-1]
			if len(line) != 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return string(line), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return string(line), nil
		default:
			return "", err
		}
	}
}
