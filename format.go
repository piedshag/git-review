package main

import (
	"encoding/json"
	"errors"
	"io"
)

type OutputFormat string

const (
	FormatMarkdown OutputFormat = "markdown"
	FormatJSON     OutputFormat = "json"
)

func parseOutputFormat(value string) (OutputFormat, error) {
	format := OutputFormat(value)
	switch format {
	case FormatMarkdown, FormatJSON:
		return format, nil
	default:
		return "", errors.New("format must be markdown or json")
	}
}

func writeReview(writer io.Writer, format OutputFormat, review Review) error {
	review.Findings = sortedFindings(review.Findings)
	switch format {
	case FormatMarkdown:
		_, err := io.WriteString(writer, renderMarkdown(review)+"\n")
		return err
	case FormatJSON:
		if review.Findings == nil {
			review.Findings = []Finding{}
		}
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(review)
	default:
		return errors.New("unsupported output format")
	}
}
