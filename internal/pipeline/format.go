package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/piedshag/git-review/internal/review"
)

func Write(writer io.Writer, format review.OutputFormat, result RunResult) error {
	switch format {
	case review.FormatJSON:
		for index := range result.Nodes {
			if result.Nodes[index].Review != nil && result.Nodes[index].Review.Findings == nil {
				normalized := *result.Nodes[index].Review
				normalized.Findings = []review.Finding{}
				result.Nodes[index].Review = &normalized
			}
		}
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	case review.FormatMarkdown:
		selected, ok := result.Selected()
		if !ok {
			_, err := fmt.Fprintf(writer, "# Review run\n\nOutput agent %q did not produce a review.\n", result.Output)
			return err
		}
		return review.Write(writer, format, selected)
	default:
		return errors.New("unsupported output format")
	}
}
