package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const maxInstructionsBytes = 1024 * 1024

func loadInstructions(inline, filename string, stdin io.Reader) (string, error) {
	if strings.TrimSpace(filename) == "" {
		return strings.TrimSpace(inline), nil
	}
	var reader io.Reader
	var file *os.File
	if filename == "-" {
		reader = stdin
	} else {
		var err error
		file, err = os.Open(filename)
		if err != nil {
			return "", fmt.Errorf("read instructions: %w", err)
		}
		defer file.Close()
		reader = file
	}
	contents, err := io.ReadAll(io.LimitReader(reader, maxInstructionsBytes+1))
	if err != nil {
		return "", fmt.Errorf("read instructions: %w", err)
	}
	if len(contents) > maxInstructionsBytes {
		return "", errors.New("review instructions exceed 1 MiB")
	}
	return strings.TrimSpace(string(contents)), nil
}
